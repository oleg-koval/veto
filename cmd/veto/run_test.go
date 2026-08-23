package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteOutputFile(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(old) })

	require.NoError(t, writeOutputFile("result.md", "```md\nhello\n```", false))
	data, err := os.ReadFile(filepath.Join(dir, "result.md"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))

	info, err := os.Stat(filepath.Join(dir, "result.md"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	require.ErrorContains(t, writeOutputFile("result.md", "replacement", false), "already exists")
	require.NoError(t, writeOutputFile("result.md", "replacement", true))
	data, err = os.ReadFile(filepath.Join(dir, "result.md"))
	require.NoError(t, err)
	assert.Equal(t, "replacement\n", string(data))
}

func TestWriteOutputFileRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"../outside.txt", "/tmp/absolute.txt", ".env", "nested/.credentials"} {
		t.Run(path, func(t *testing.T) {
			require.Error(t, writeOutputFile(path, "secret", false))
		})
	}
}

func TestWriteOutputFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.txt")
	link := filepath.Join(dir, "result.txt")
	require.NoError(t, os.Symlink(target, link))
	old, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(old) })

	require.ErrorContains(t, writeOutputFile("result.txt", "secret", true), "symbolic links")
	_, err = os.Stat(target)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
