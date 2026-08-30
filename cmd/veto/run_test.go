package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateExecutionResultRejectsTruncation(t *testing.T) {
	err := validateExecutionResult(executor.Result{
		Output:       "partial output",
		FinishReason: "max_output_tokens",
		Truncated:    true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "truncated")
	assert.ErrorContains(t, err, "max_output_tokens")
	assert.ErrorContains(t, err, "--max-output-tokens")
}

func TestValidateExecutionResultAcceptsCompleteOutput(t *testing.T) {
	require.NoError(t, validateExecutionResult(executor.Result{Output: "complete output"}))
}

func TestValidateExecutionResultPreservesProviderError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	err := validateExecutionResult(executor.Result{Error: providerErr})

	assert.ErrorIs(t, err, providerErr)
}

func TestHasEffectiveToolsUsesRuntimeCapabilities(t *testing.T) {
	assert.False(t, hasEffectiveTools(executor.NewOpenAIExecutor("key", "gpt-4.1")))
	assert.True(t, hasEffectiveTools(executor.NewClaudeCLIExecutor("sonnet")))
}

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
