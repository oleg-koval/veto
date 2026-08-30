// Package hermes contains the native Hermes plugin shipped inside Veto.
package hermes

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

//go:embed veto/__init__.py veto/runtime.py veto/plugin.yaml veto/README.md
var assets embed.FS

var managedFiles = []string{"README.md", "__init__.py", "plugin.yaml", "runtime.py"}

// State describes whether all embedded plugin files are installed.
type State struct {
	Installed int
	Missing   int
	Modified  int
}

// Install writes the plugin to <HERMES_HOME>/plugins/veto. Existing files are
// never replaced unless force is explicit.
func Install(home string, force bool) (State, error) {
	dir, err := pluginDir(home)
	if err != nil {
		return State{}, err
	}
	contents := make(map[string][]byte, len(managedFiles))
	for _, name := range managedFiles {
		target := filepath.Join(dir, name)
		data, readErr := fs.ReadFile(assets, "veto/"+name)
		if readErr != nil {
			return State{}, readErr
		}
		contents[name] = data
		if info, statErr := os.Lstat(target); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return State{}, fmt.Errorf("refusing unsafe Hermes plugin target %s", target)
			}
			current, readErr := os.ReadFile(target)
			if readErr != nil {
				return State{}, readErr
			}
			if !same(current, data) && !force {
				return State{}, fmt.Errorf("%s already exists and is not managed by this Veto version; use --force to replace it", target)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return State{}, statErr
		}
	}
	for _, name := range managedFiles {
		target := filepath.Join(dir, name)
		if current, readErr := os.ReadFile(target); readErr == nil && same(current, contents[name]) {
			continue
		}
		if err := writeAtomic(target, contents[name]); err != nil {
			return State{}, err
		}
	}
	return Status(home)
}

// Status compares installed files with the plugin embedded in this build.
func Status(home string) (State, error) {
	dir, err := pluginDir(home)
	if err != nil {
		return State{}, err
	}
	var state State
	for _, name := range managedFiles {
		target := filepath.Join(dir, name)
		info, statErr := os.Lstat(target)
		if errors.Is(statErr, os.ErrNotExist) {
			state.Missing++
			continue
		}
		if statErr != nil {
			return State{}, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			state.Modified++
			continue
		}
		got, readErr := os.ReadFile(target)
		if readErr != nil {
			return State{}, readErr
		}
		want, readErr := fs.ReadFile(assets, "veto/"+name)
		if readErr != nil {
			return State{}, readErr
		}
		if same(got, want) {
			state.Installed++
		} else {
			state.Modified++
		}
	}
	return state, nil
}

// Uninstall removes only files that still exactly match this build. Hermes
// configuration is deliberately left unchanged.
func Uninstall(home string) (State, error) {
	state, err := Status(home)
	if err != nil {
		return State{}, err
	}
	if state.Modified != 0 {
		return state, errors.New("refusing to remove modified Hermes plugin files")
	}
	dir, err := pluginDir(home)
	if err != nil {
		return State{}, err
	}
	files := append([]string(nil), managedFiles...)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, name := range files {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
	}
	return Status(home)
}

func pluginDir(home string) (string, error) {
	if strings.TrimSpace(home) == "" || !filepath.IsAbs(home) {
		return "", errors.New("Hermes home must be an absolute path")
	}
	dir := filepath.Join(home, "plugins", "veto")
	for _, current := range []string{home, filepath.Join(home, "plugins"), dir} {
		if info, err := os.Lstat(current); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("Hermes plugin directory is unsafe: %s", current)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return dir, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if info, err := os.Lstat(dir); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return fmt.Errorf("Hermes plugin directory is unsafe: %s", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".veto-plugin-*.tmp")
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
