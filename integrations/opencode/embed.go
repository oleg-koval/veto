// Package opencode contains the self-contained OpenCode integration shipped
// inside the Veto binary.
package opencode

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/plugins/*.js assets/lib/*.js assets/commands/*.md
var assets embed.FS

var managedFiles = []string{
	"veto/veto-core.js",
	"plugins/veto.js",
	"commands/veto-off.md",
	"commands/veto-on.md",
	"commands/veto-route.md",
	"commands/veto-status.md",
}

// State describes whether all embedded integration files are installed.
type State struct {
	Installed int
	Missing   int
	Modified  int
}

// Install writes the integration into an isolated OpenCode config directory.
// Existing files are never replaced unless force is explicit.
func Install(configDir string, force bool) (State, error) {
	if err := validateConfigDir(configDir); err != nil {
		return State{}, err
	}
	contents := make(map[string][]byte, len(managedFiles))
	for _, rel := range managedFiles {
		if err := validateManagedParents(configDir, rel); err != nil {
			return State{}, err
		}
		target := filepath.Join(configDir, filepath.FromSlash(rel))
		data, err := readAsset(rel)
		if err != nil {
			return State{}, err
		}
		contents[rel] = data
		if info, err := os.Lstat(target); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return State{}, fmt.Errorf("refusing unsafe OpenCode integration target %s", target)
			}
			current, readErr := os.ReadFile(target)
			if readErr != nil {
				return State{}, readErr
			}
			if !same(current, data) && !force {
				return State{}, fmt.Errorf("%s already exists and is not managed by this Veto version; use --force to replace it", target)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
	}
	for _, rel := range managedFiles {
		if err := validateManagedParents(configDir, rel); err != nil {
			return State{}, err
		}
		target := filepath.Join(configDir, filepath.FromSlash(rel))
		data := contents[rel]
		if current, err := os.ReadFile(target); err == nil && same(current, data) {
			continue
		}
		if err := writeAtomic(target, data); err != nil {
			return State{}, err
		}
	}
	return Status(configDir)
}

// Status compares installed files with the integration embedded in this build.
func Status(configDir string) (State, error) {
	if err := validateConfigDir(configDir); err != nil {
		return State{}, err
	}
	var state State
	for _, rel := range managedFiles {
		if err := validateManagedParents(configDir, rel); err != nil {
			return State{}, err
		}
		target := filepath.Join(configDir, filepath.FromSlash(rel))
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			state.Missing++
			continue
		}
		if err != nil {
			return State{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			state.Modified++
			continue
		}
		got, err := os.ReadFile(target)
		if err != nil {
			return State{}, err
		}
		want, err := readAsset(rel)
		if err != nil {
			return State{}, err
		}
		if same(got, want) {
			state.Installed++
		} else {
			state.Modified++
		}
	}
	return state, nil
}

// Uninstall removes only files that still exactly match this build. Modified
// files are preserved and reported as an error.
func Uninstall(configDir string) (State, error) {
	state, err := Status(configDir)
	if err != nil {
		return State{}, err
	}
	if state.Modified != 0 {
		return state, errors.New("refusing to remove modified OpenCode integration files")
	}
	files := append([]string(nil), managedFiles...)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, rel := range files {
		if err := validateManagedParents(configDir, rel); err != nil {
			return State{}, err
		}
		target := filepath.Join(configDir, filepath.FromSlash(rel))
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
	}
	return Status(configDir)
}

func validateConfigDir(path string) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return errors.New("OpenCode config directory must be an absolute path")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("OpenCode config directory must be a real directory, not a symlink")
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validateManagedParents(configDir, rel string) error {
	parent := filepath.Dir(filepath.FromSlash(rel))
	if parent == "." {
		return nil
	}
	current := configDir
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("OpenCode integration directory is unsafe: %s", current)
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if info, err := os.Lstat(dir); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return fmt.Errorf("OpenCode integration directory is unsafe: %s", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".veto-integration-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func same(a, b []byte) bool { return sha256.Sum256(a) == sha256.Sum256(b) }

func readAsset(rel string) ([]byte, error) {
	if rel == "veto/veto-core.js" {
		return fs.ReadFile(assets, "assets/lib/veto-core.js")
	}
	return fs.ReadFile(assets, "assets/"+rel)
}
