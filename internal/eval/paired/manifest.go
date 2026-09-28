package paired

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/oleg-koval/veto/pkg/shadow"
)

const (
	ManifestVersion         = 1
	maxPrivateManifestBytes = 1 << 20
)

// Manifest is a private, explicitly supplied join between a frozen task and a
// route-time witness. It contains raw task text and model names; never append
// it to shadow evidence or commit it to a repository.
type Manifest struct {
	Version         int                        `json:"version"`
	RouteID         string                     `json:"route_id"`
	TaskKind        router.TaskKind            `json:"task_kind"`
	Risk            router.Risk                `json:"risk"`
	Objective       string                     `json:"objective"`
	Criteria        []string                   `json:"criteria"`
	MaxOutputTokens int                        `json:"max_output_tokens"`
	TimeoutMillis   int64                      `json:"timeout_millis"`
	Witness         router.PrivateRouteWitness `json:"witness"`
}

// ValidateManifest checks the task fingerprint and every route-time binding
// against the redacted route. The witness must come from the opt-in route-time
// recorder: a self-authored or tampered witness cannot be authenticated here.
func ValidateManifest(dataset shadow.Dataset, manifest Manifest) (Trial, error) {
	if manifest.Version != ManifestVersion || manifest.Witness.Version != router.PrivateWitnessVersion {
		return Trial{}, errors.New("paired manifest: unsupported version")
	}
	if manifest.RouteID == "" || manifest.RouteID != manifest.Witness.RouteID ||
		manifest.TaskKind != manifest.Witness.TaskKind || manifest.Risk != manifest.Witness.Risk {
		return Trial{}, errors.New("paired manifest: route metadata does not match witness")
	}
	if manifest.TimeoutMillis <= 0 || manifest.TimeoutMillis > int64(maxRunTimeout/time.Millisecond) {
		return Trial{}, errors.New("paired manifest: timeout is outside the allowed range")
	}
	var comparison *shadow.RouteComparison
	for index := range dataset.Routes {
		if dataset.Routes[index].Comparison.RouteID == manifest.RouteID {
			if comparison != nil {
				return Trial{}, errors.New("paired manifest: duplicate route comparison")
			}
			comparison = &dataset.Routes[index].Comparison
		}
	}
	if comparison == nil || !manifest.Witness.ObservedAt.Equal(comparison.ObservedAt) ||
		string(manifest.TaskKind) != comparison.TaskKind || string(manifest.Risk) != comparison.Risk {
		return Trial{}, errors.New("paired manifest: witness does not match route comparison")
	}
	if len(manifest.Witness.Bindings) != len(comparison.Candidates) {
		return Trial{}, errors.New("paired manifest: candidate bindings do not match comparison")
	}
	modelsByKey := make(map[string]string, len(manifest.Witness.Bindings))
	models := make(map[string]bool, len(manifest.Witness.Bindings))
	for index, binding := range manifest.Witness.Bindings {
		derived, err := router.PrivateCandidateKey(manifest.Witness.RouteKey, binding.Model)
		if err != nil || binding.Key != derived || binding.Key != comparison.Candidates[index].Key ||
			strings.TrimSpace(binding.Model) == "" || models[binding.Model] {
			return Trial{}, errors.New("paired manifest: candidate bindings do not match comparison")
		}
		modelsByKey[binding.Key] = binding.Model
		models[binding.Model] = true
	}
	if len(modelsByKey) != len(manifest.Witness.Bindings) {
		return Trial{}, errors.New("paired manifest: duplicate candidate binding")
	}
	task := router.TaskSpec{
		Kind: manifest.TaskKind, Risk: manifest.Risk, Objective: manifest.Objective,
		SuccessCriteria: manifest.Criteria, ExecutionMaxOutputTokens: manifest.MaxOutputTokens,
	}
	want, err := hex.DecodeString(manifest.Witness.TaskFingerprint)
	if err != nil || len(want) != 32 {
		return Trial{}, errors.New("paired manifest: invalid task fingerprint")
	}
	fingerprint, err := router.PrivateTaskFingerprint(manifest.Witness.RouteKey, task)
	if err != nil {
		return Trial{}, errors.New("paired manifest: invalid route key")
	}
	got, _ := hex.DecodeString(fingerprint)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return Trial{}, errors.New("paired manifest: frozen task does not match witness")
	}
	trial := Trial{
		RouteID: manifest.RouteID, TaskKind: string(manifest.TaskKind), Risk: string(manifest.Risk),
		Objective: manifest.Objective, Criteria: append([]string(nil), manifest.Criteria...),
		ModelsByKey: modelsByKey, MaxOutputTokens: manifest.MaxOutputTokens,
		Timeout: time.Duration(manifest.TimeoutMillis) * time.Millisecond,
	}
	if _, _, err := validateTrial(dataset, trial); err != nil {
		return Trial{}, err
	}
	return trial, nil
}

// ReplayManifest is the provenance-checked entry point for paired evaluation.
// It returns only redacted labels and never persists them.
func ReplayManifest(ctx context.Context, dataset shadow.Dataset, manifest Manifest, runner Runner, grader Grader) ([]shadow.Event, error) {
	trial, err := ValidateManifest(dataset, manifest)
	if err != nil {
		return nil, err
	}
	return Replay(ctx, dataset, trial, runner, grader)
}

// SaveManifest creates one validated private file without overwriting an
// existing path. Callers must choose an absolute path in a trusted, stable
// private directory outside the repo. A failed write may leave a partial 0600
// file; it is never removed automatically because the path could be replaced.
func SaveManifest(path string, dataset shadow.Dataset, manifest Manifest) error {
	if _, err := ValidateManifest(dataset, manifest); err != nil {
		return err
	}
	root, name, err := openPrivateRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || len(encoded)+1 > maxPrivateManifestBytes {
		return errors.New("paired manifest: invalid or oversized private data")
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("paired manifest: create private file failed")
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return errors.New("paired manifest: write private file failed")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("paired manifest: sync private file failed")
	}
	if err := file.Close(); err != nil {
		return errors.New("paired manifest: close private file failed")
	}
	return nil
}

// LoadManifest accepts only a bounded regular file with private permissions
// in a private directory. Unknown fields and trailing JSON are rejected.
func LoadManifest(path string) (Manifest, error) {
	root, name, err := openPrivateRoot(path)
	if err != nil {
		return Manifest{}, err
	}
	defer root.Close()
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > maxPrivateManifestBytes {
		return Manifest{}, errors.New("paired manifest: unsafe private file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return Manifest{}, errors.New("paired manifest: open private file failed")
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() > maxPrivateManifestBytes {
		return Manifest{}, errors.New("paired manifest: private file changed while opening")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxPrivateManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, errors.New("paired manifest: invalid private JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Manifest{}, errors.New("paired manifest: trailing private JSON")
	}
	if manifest.Version != ManifestVersion || manifest.Witness.Version != router.PrivateWitnessVersion {
		return Manifest{}, errors.New("paired manifest: unsupported version")
	}
	return manifest, nil
}

// openPrivateRoot pins the checked directory for the final file operation.
// The caller must trust other code with access to this directory not to move
// it into a repository while the operation is in progress.
func openPrivateRoot(path string) (*os.Root, string, error) {
	if !filepath.IsAbs(path) {
		return nil, "", errors.New("paired manifest: absolute private path is required")
	}
	parentPath := filepath.Dir(path)
	parent, err := os.Lstat(parentPath)
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return nil, "", errors.New("paired manifest: private parent directory is required")
	}
	resolved, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		return nil, "", errors.New("paired manifest: cannot resolve private directory")
	}
	for _, root := range []string{parentPath, resolved} {
		for dir := root; ; dir = filepath.Dir(dir) {
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				return nil, "", errors.New("paired manifest: repository paths are not allowed")
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, "", errors.New("paired manifest: cannot inspect repository boundary")
			}
			if next := filepath.Dir(dir); next == dir {
				break
			}
		}
	}
	root, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, "", errors.New("paired manifest: cannot open private directory")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, opened) {
		_ = root.Close()
		return nil, "", errors.New("paired manifest: private directory changed while opening")
	}
	return root, filepath.Base(path), nil
}
