package paired

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
)

const (
	DefaultCaptureLimit  = 50
	defaultReplayTimeout = 10 * time.Minute
	maxCaptureLimit      = 500
)

// FileCaptureRecorder persists only when explicitly installed by CLI
// configuration. It stores private manifests, never task output or credentials.
type FileCaptureRecorder struct {
	mu       sync.Mutex
	dir      string
	maxFiles int
}

// NewFileCaptureRecorder requires an existing private directory outside every
// Git worktree. Capture is intentionally not constructed by default.
func NewFileCaptureRecorder(dir string, maxFiles int) (*FileCaptureRecorder, error) {
	if maxFiles <= 0 || maxFiles > maxCaptureLimit {
		return nil, errors.New("paired capture: retention must be between 1 and 500 manifests")
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("paired capture: absolute private directory is required")
	}
	if _, _, err := openPrivateRoot(filepath.Join(dir, ".capture-check")); err != nil {
		return nil, err
	}
	recorder := &FileCaptureRecorder{dir: dir, maxFiles: maxFiles}
	if err := recorder.prune(); err != nil {
		return nil, err
	}
	return recorder, nil
}

// RecordPrivateRouteCapture saves the replayable task beside its exact
// route-time identity and tool snapshot. Tasks without grading criteria or an
// explicit output budget are not useful paired trials and are skipped.
func (r *FileCaptureRecorder) RecordPrivateRouteCapture(witness router.PrivateRouteWitness, task router.TaskSpec) error {
	if r == nil {
		return errors.New("paired capture: recorder is unavailable")
	}
	if task.Source != "user" || strings.TrimSpace(task.Objective) == "" || len(task.SuccessCriteria) == 0 || task.ExecutionMaxOutputTokens <= 0 {
		return nil
	}
	manifest := Manifest{
		Version: ManifestVersion, RouteID: witness.RouteID, TaskKind: task.Kind,
		Risk: task.Risk, Objective: task.Objective,
		Criteria:        append([]string(nil), task.SuccessCriteria...),
		MaxOutputTokens: task.ExecutionMaxOutputTokens,
		TimeoutMillis:   defaultReplayTimeout.Milliseconds(), Witness: witness,
	}
	if err := validateCapturedManifest(manifest); err != nil {
		return err
	}
	path := filepath.Join(r.dir, "manifest-"+witness.RouteID+".json")
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := savePrivateManifest(path, manifest); err != nil {
		return err
	}
	return r.prune()
}

func validateCapturedManifest(manifest Manifest) error {
	if manifest.Version != ManifestVersion || manifest.Witness.Version != router.PrivateWitnessVersion ||
		!isPrivateRouteID(manifest.RouteID) || manifest.RouteID != manifest.Witness.RouteID ||
		manifest.TaskKind != manifest.Witness.TaskKind || manifest.Risk != manifest.Witness.Risk ||
		manifest.TimeoutMillis <= 0 || manifest.TimeoutMillis > int64(maxRunTimeout/time.Millisecond) ||
		strings.TrimSpace(manifest.Objective) == "" || len(manifest.Criteria) == 0 || manifest.MaxOutputTokens <= 0 {
		return errors.New("paired capture: incomplete private manifest")
	}
	for _, criterion := range manifest.Criteria {
		if strings.TrimSpace(criterion) == "" {
			return errors.New("paired capture: empty acceptance criterion")
		}
	}
	want, err := router.PrivateTaskFingerprint(manifest.Witness.RouteKey, router.TaskSpec{
		Kind: manifest.TaskKind, Risk: manifest.Risk, Objective: manifest.Objective,
		SuccessCriteria: manifest.Criteria, ExecutionMaxOutputTokens: manifest.MaxOutputTokens,
	})
	if err != nil || want != manifest.Witness.TaskFingerprint {
		return errors.New("paired capture: task does not match route-time witness")
	}
	if len(manifest.Witness.Bindings) == 0 {
		return errors.New("paired capture: route has no candidate bindings")
	}
	seen := make(map[string]bool, len(manifest.Witness.Bindings))
	for _, binding := range manifest.Witness.Bindings {
		key, err := router.PrivateCandidateKey(manifest.Witness.RouteKey, binding.Model, binding.Identity, binding.Tools)
		if err != nil || strings.TrimSpace(binding.Model) == "" || key != binding.Key || seen[key] || !binding.Tools.Known ||
			strings.TrimSpace(binding.Identity.Provider) == "" || strings.TrimSpace(binding.Identity.Model) == "" ||
			strings.TrimSpace(binding.Identity.Runtime) == "" || strings.TrimSpace(binding.Identity.Source) == "" {
			return errors.New("paired capture: invalid route-time candidate binding")
		}
		seen[key] = true
	}
	return nil
}

func isPrivateRouteID(value string) bool {
	if len(value) != 26 || !strings.HasPrefix(value, "r-") {
		return false
	}
	for _, char := range value[2:] {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func savePrivateManifest(path string, manifest Manifest) error {
	root, name, err := openPrivateRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || len(encoded)+1 > maxPrivateManifestBytes {
		return errors.New("paired capture: invalid or oversized private manifest")
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("paired capture: create private manifest failed")
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return errors.New("paired capture: write private manifest failed")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("paired capture: sync private manifest failed")
	}
	if err := file.Close(); err != nil {
		return errors.New("paired capture: close private manifest failed")
	}
	return nil
}

func (r *FileCaptureRecorder) prune() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return errors.New("paired capture: inspect private retention directory failed")
	}
	type captureFile struct {
		name string
		mod  time.Time
	}
	files := make([]captureFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "manifest-r-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("paired capture: unsafe file in retention directory")
		}
		files = append(files, captureFile{name: entry.Name(), mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].mod.Equal(files[j].mod) {
			return files[i].name < files[j].name
		}
		return files[i].mod.Before(files[j].mod)
	})
	for len(files) > r.maxFiles {
		if err := os.Remove(filepath.Join(r.dir, files[0].name)); err != nil {
			return errors.New("paired capture: prune expired private manifest failed")
		}
		files = files[1:]
	}
	return nil
}
