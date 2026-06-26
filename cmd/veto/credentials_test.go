package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, saveCredential("ANTHROPIC_API_KEY", "sk-ant-test"))

	creds, err := loadCredentials()
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-test", creds["ANTHROPIC_API_KEY"])
}

func TestSaveCredential_MergesMultipleKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, saveCredential("ANTHROPIC_API_KEY", "sk-ant-1"))
	require.NoError(t, saveCredential("OPENAI_API_KEY", "sk-openai-1"))

	creds, err := loadCredentials()
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-1", creds["ANTHROPIC_API_KEY"])
	assert.Equal(t, "sk-openai-1", creds["OPENAI_API_KEY"])
}

func TestSaveCredential_OverwritesExistingKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, saveCredential("ANTHROPIC_API_KEY", "old-key"))
	require.NoError(t, saveCredential("ANTHROPIC_API_KEY", "new-key"))

	creds, err := loadCredentials()
	require.NoError(t, err)
	assert.Equal(t, "new-key", creds["ANTHROPIC_API_KEY"])
}

func TestLoadCredentials_FileNotExist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	creds, err := loadCredentials()
	require.NoError(t, err)
	assert.Empty(t, creds)
}

func TestLoadCredentials_CorruptJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := credentialsPath()
	require.NoError(t, os.MkdirAll(path[:len(path)-len("/credentials.json")], 0700))
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0600))

	_, err := loadCredentials()
	require.Error(t, err)
}

func TestGetKey_EnvVarWins(t *testing.T) {
	t.Setenv("MY_KEY", "from-env")
	creds := credentials{"MY_KEY": "from-file"}
	assert.Equal(t, "from-env", getKey("MY_KEY", creds))
}

func TestGetKey_FallsBackToFile(t *testing.T) {
	os.Unsetenv("MY_KEY_ABSENT")
	creds := credentials{"MY_KEY_ABSENT": "from-file"}
	assert.Equal(t, "from-file", getKey("MY_KEY_ABSENT", creds))
}

func TestGetKey_EmptyWhenNeitherSet(t *testing.T) {
	os.Unsetenv("TOTALLY_MISSING")
	assert.Empty(t, getKey("TOTALLY_MISSING", credentials{}))
}

func TestCredentialsPath_ContainsDotVeto(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := credentialsPath()
	assert.Contains(t, path, ".veto")
	assert.Contains(t, path, "credentials.json")
}

func TestSaveCredential_FilePermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, saveCredential("ANTHROPIC_API_KEY", "sk-ant-test"))

	info, err := os.Stat(credentialsPath())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
