package hermes

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallStatusAndUninstall(t *testing.T) {
	home := filepath.Join(t.TempDir(), "hermes")
	state, err := Install(home, false)
	require.NoError(t, err)
	assert.Equal(t, State{Installed: len(managedFiles)}, state)
	for _, name := range managedFiles {
		info, err := os.Stat(filepath.Join(home, "plugins", "veto", name))
		require.NoError(t, err)
		if runtime.GOOS != "windows" {
			assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
		}
	}
	state, err = Uninstall(home)
	require.NoError(t, err)
	assert.Equal(t, len(managedFiles), state.Missing)
}

func TestInstallPreflightsCollisionAndPreservesModifiedFiles(t *testing.T) {
	home := filepath.Join(t.TempDir(), "hermes")
	target := filepath.Join(home, "plugins", "veto", "runtime.py")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0700))
	require.NoError(t, os.WriteFile(target, []byte("user file"), 0600))

	_, err := Install(home, false)
	assert.ErrorContains(t, err, "already exists")
	_, statErr := os.Stat(filepath.Join(home, "plugins", "veto", "plugin.yaml"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)

	_, err = Install(home, true)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(target, []byte("modified"), 0600))
	_, err = Uninstall(home)
	assert.ErrorContains(t, err, "modified")
	got, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "modified", string(got))
}

func TestInstallRejectsSymlinkedPluginDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	home := filepath.Join(t.TempDir(), "hermes")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "plugins"), 0700))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(home, "plugins", "veto")))
	_, err := Install(home, true)
	assert.ErrorContains(t, err, "unsafe")
}

func TestStatusRejectsSymlinkedHermesHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	link := filepath.Join(t.TempDir(), "hermes")
	require.NoError(t, os.Symlink(t.TempDir(), link))
	_, err := Status(link)
	assert.ErrorContains(t, err, "unsafe")
}
