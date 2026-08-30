package opencode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallStatusAndUninstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "opencode")
	state, err := Install(dir, false)
	require.NoError(t, err)
	assert.Equal(t, State{Installed: len(managedFiles)}, state)

	for _, rel := range managedFiles {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		require.NoError(t, err)
		if runtime.GOOS != "windows" {
			assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
		}
	}

	state, err = Status(dir)
	require.NoError(t, err)
	assert.Equal(t, State{Installed: len(managedFiles)}, state)

	state, err = Uninstall(dir)
	require.NoError(t, err)
	assert.Equal(t, len(managedFiles), state.Missing)
}

func TestInstallPreservesCollisionAndModifiedUninstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "opencode")
	target := filepath.Join(dir, "plugins", "veto.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0700))
	require.NoError(t, os.WriteFile(target, []byte("user file"), 0600))

	_, err := Install(dir, false)
	assert.ErrorContains(t, err, "already exists")
	_, statErr := os.Stat(filepath.Join(dir, "veto", "veto-core.js"))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "collision preflight must not leave a partial install")
	got, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "user file", string(got))

	_, err = Install(dir, true)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(target, []byte("modified"), 0600))
	_, err = Uninstall(dir)
	assert.ErrorContains(t, err, "modified")
	got, readErr = os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "modified", string(got))
}

func TestInstallRejectsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	dir := filepath.Join(t.TempDir(), "opencode")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "plugins"), 0700))
	target := filepath.Join(dir, "plugins", "veto.js")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "outside"), target))
	_, err := Install(dir, true)
	assert.ErrorContains(t, err, "unsafe")
}

func TestStatusAndUninstallRejectIntermediateSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	dir := filepath.Join(t.TempDir(), "opencode")
	external := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0700))
	data, err := readAsset("plugins/veto.js")
	require.NoError(t, err)
	externalFile := filepath.Join(external, "veto.js")
	require.NoError(t, os.WriteFile(externalFile, data, 0600))
	require.NoError(t, os.Symlink(external, filepath.Join(dir, "plugins")))

	_, err = Status(dir)
	assert.ErrorContains(t, err, "unsafe")
	_, err = Uninstall(dir)
	assert.ErrorContains(t, err, "unsafe")
	assert.FileExists(t, externalFile)
}
