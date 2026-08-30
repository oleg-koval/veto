package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	opencodert "github.com/oleg-koval/veto/pkg/opencode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeConnectStatusDisconnectPreservesOtherConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"1.18.5"}`))
		case "/provider":
			_, _ = w.Write([]byte(`{"all":[{"id":"anthropic","models":{"sonnet":{"id":"claude-sonnet"}}}],"connected":["anthropic"],"default":{}}`))
		}
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"routing":{"pinned_models":["sol"]}}`), 0600))
	deps := opencodert.Dependencies{Do: server.Client().Do}
	var stdout, stderr bytes.Buffer

	code := runOpenCodeCommand([]string{"connect", "--server", server.URL}, &stdout, &stderr, deps, configPath)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "1 model(s)")
	config, configured, err := loadOpenCodeConfig(configPath)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, opencodert.ModeAttach, config.Mode)

	stdout.Reset()
	stderr.Reset()
	code = runOpenCodeCommand([]string{"status", "--json"}, &stdout, &stderr, deps, configPath)
	require.Equal(t, 0, code, stderr.String())
	var status opencodert.Discovery
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &status))
	assert.Equal(t, "1.18.5", status.Version)
	assert.Len(t, status.Models, 1)

	stdout.Reset()
	stderr.Reset()
	code = runOpenCodeCommand([]string{"disconnect"}, &stdout, &stderr, deps, configPath)
	require.Equal(t, 0, code, stderr.String())
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "pinned_models")
	assert.NotContains(t, string(data), "opencode")
}

func TestOpenCodeConfigRefusesMalformedFileAndSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{broken`), 0600))
	err := saveOpenCodeConfig(path, opencodert.Config{Mode: opencodert.ModeCLI})
	require.Error(t, err)
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, `{broken`, string(data))

	target := filepath.Join(t.TempDir(), "target.json")
	require.NoError(t, os.WriteFile(target, []byte(`{}`), 0600))
	link := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.Symlink(target, link))
	assert.Error(t, saveOpenCodeConfig(link, opencodert.Config{Mode: opencodert.ModeCLI}))

	nullPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(nullPath, []byte(`null`), 0600))
	assert.Error(t, saveOpenCodeConfig(nullPath, opencodert.Config{Mode: opencodert.ModeCLI}))
}

func TestOpenCodeConnectDefaultsToCLIFallback(t *testing.T) {
	var calls [][]string
	deps := opencodert.Dependencies{
		LookPath: func(string) (string, error) { return `C:\Program Files\OpenCode\opencode.exe`, nil },
		Run: func(_ context.Context, path string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{path}, args...))
			if slices.Equal(args, []string{"--version"}) {
				return []byte("1.18.5"), nil
			}
			return []byte("openrouter/openai/gpt-5"), nil
		},
	}
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), ".veto", "config.json")
	code := runOpenCodeCommand([]string{"connect"}, &stdout, &stderr, deps, configPath)
	require.Equal(t, 0, code, stderr.String())
	require.Len(t, calls, 2)
	assert.Equal(t, `C:\Program Files\OpenCode\opencode.exe`, calls[0][0])
	config, configured, err := loadOpenCodeConfig(configPath)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, opencodert.ModeCLI, config.Mode)
}

func TestOpenCodeConnectRejectsHostileServerWithoutHTTP(t *testing.T) {
	called := false
	deps := opencodert.Dependencies{Do: func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	code := runOpenCodeCommand([]string{"connect", "--server", "http://example.com:4096"}, &stdout, &stderr, deps, filepath.Join(t.TempDir(), "config.json"))
	assert.Equal(t, 1, code)
	assert.False(t, called)
}

func TestOpenCodeProcessJSONHasNoPendingSkillNotice(t *testing.T) {
	if os.Getenv("VETO_TEST_OPENCODE_PROCESS") == "1" {
		os.Args = []string{"veto", "opencode", "status", "--json"}
		main()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".veto"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".veto", "config.json"), []byte(`{"opencode":{"mode":"cli"}}`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".claude", "skills", "pending.md"), []byte("---\nname: pending\n---\n"), 0600))
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '1.18.5\\n'; else printf 'openai/gpt-5\\n'; fi\n"
	require.NoError(t, os.WriteFile(fake, []byte(script), 0755))

	command := exec.Command(os.Args[0], "-test.run=^TestOpenCodeProcessJSONHasNoPendingSkillNotice$")
	command.Env = []string{"VETO_TEST_OPENCODE_PROCESS=1", "HOME=" + home, "PATH=" + binDir}
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	var status opencodert.Discovery
	require.NoError(t, json.NewDecoder(bytes.NewReader(output)).Decode(&status), string(output))
	assert.Equal(t, "1.18.5", status.Version)
	assert.NotContains(t, string(output), "notice:")
}

func TestOpenCodePluginCommandLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "opencode")
	var stdout, stderr bytes.Buffer

	code := runOpenCodeCommand([]string{"plugin", "install", "--config-dir", dir}, &stdout, &stderr, opencodert.Dependencies{}, "")
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "6 files")
	assert.FileExists(t, filepath.Join(dir, "plugins", "veto.js"))

	stdout.Reset()
	stderr.Reset()
	code = runOpenCodeCommand([]string{"plugin", "status", "--config-dir", dir}, &stdout, &stderr, opencodert.Dependencies{}, "")
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "installed and current")

	stdout.Reset()
	stderr.Reset()
	code = runOpenCodeCommand([]string{"plugin", "uninstall", "--config-dir", dir}, &stdout, &stderr, opencodert.Dependencies{}, "")
	require.Equal(t, 0, code, stderr.String())
	assert.NoFileExists(t, filepath.Join(dir, "plugins", "veto.js"))
}

func TestOpenCodePluginConfigDirFollowsOpenCodeXDGContract(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err := openCodeConfigDir("")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(xdg, "opencode"), got)

	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("OPENCODE_CONFIG_DIR", explicit)
	got, err = openCodeConfigDir("")
	require.NoError(t, err)
	assert.Equal(t, explicit, got)
}
