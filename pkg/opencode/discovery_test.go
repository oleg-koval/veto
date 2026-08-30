package opencode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverAttachedServerUsesConnectedProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"1.18.5"}`))
		case "/provider":
			_, _ = w.Write([]byte(`{"all":[{"id":"anthropic","models":{"sonnet":{"id":"claude-sonnet"}}},{"id":"openai","models":{"gpt":{"id":"gpt-5"}}}],"connected":["anthropic"],"default":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Discover(context.Background(), Config{Mode: ModeAttach, Server: loopbackURL(t, server.URL)}, testDeps(server.Client()))
	require.NoError(t, err)
	assert.Equal(t, "1.18.5", result.Version)
	assert.Equal(t, []Model{{Provider: "anthropic", ID: "claude-sonnet"}}, result.Models)
}

func TestDiscoverAttachedServerSupportsConfigProvidersEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"1.0.0"}`))
		case "/provider":
			http.NotFound(w, r)
		case "/config/providers":
			_, _ = w.Write([]byte(`{"providers":[{"id":"openrouter","models":{"x":{"id":"openai/gpt-5"}}}],"default":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Discover(context.Background(), Config{Mode: ModeAttach, Server: loopbackURL(t, server.URL)}, testDeps(server.Client()))
	require.NoError(t, err)
	assert.Equal(t, []Model{{Provider: "openrouter", ID: "openai/gpt-5"}}, result.Models)
}

func TestValidateServerURLRejectsHostileTargets(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1:4096", "http://example.com:4096", "http://127.0.0.1.evil:4096",
		"http://user:pass@127.0.0.1:4096", "http://127.0.0.1:4096/path",
		"http://127.0.0.1:4096?x=1", "http://127.0.0.1:4096/#fragment", "http://127.0.0.1",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := ValidateServerURL(raw)
			assert.Error(t, err)
		})
	}
	for _, raw := range []string{"http://127.0.0.1:4096", "http://localhost:4096", "http://[::1]:4096"} {
		t.Run(raw, func(t *testing.T) {
			_, err := ValidateServerURL(raw)
			assert.NoError(t, err)
		})
	}
}

func TestDiscoverCLIFallbackParsesModelsWithoutShell(t *testing.T) {
	var calls [][]string
	deps := Dependencies{
		LookPath: func(name string) (string, error) {
			return filepath.Join("C:\\Program Files", "OpenCode", "opencode.exe"), nil
		},
		Run: func(_ context.Context, path string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{path}, args...))
			if slices.Equal(args, []string{"--version"}) {
				return []byte("1.18.5\n"), nil
			}
			return []byte("openai/gpt-5\nanthropic/claude-sonnet\nopenai/gpt-5\n"), nil
		},
	}
	result, err := Discover(context.Background(), Config{Mode: ModeCLI}, deps)
	require.NoError(t, err)
	assert.Equal(t, []Model{{Provider: "anthropic", ID: "claude-sonnet"}, {Provider: "openai", ID: "gpt-5"}}, result.Models)
	require.Len(t, calls, 2)
	assert.Equal(t, filepath.Join("C:\\Program Files", "OpenCode", "opencode.exe"), calls[0][0])
	assert.Equal(t, []string{"models"}, calls[1][1:])
}

func TestDiscoverDiagnosesIncompatibleVersions(t *testing.T) {
	deps := Dependencies{
		LookPath: func(string) (string, error) { return "/bin/opencode", nil },
		Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if slices.Equal(args, []string{"--version"}) {
				return []byte("development-build"), nil
			}
			return nil, nil
		},
	}
	_, err := Discover(context.Background(), Config{Mode: ModeCLI}, deps)
	var incompatible *IncompatibleError
	require.ErrorAs(t, err, &incompatible)
	assert.Contains(t, err.Error(), "development-build")
}

func TestDiscoverHonorsContextTimeout(t *testing.T) {
	deps := Dependencies{
		LookPath: func(string) (string, error) { return "/bin/opencode", nil },
		Run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := Discover(ctx, Config{Mode: ModeCLI}, deps)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDiscoverAttachedServerHonorsContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := Discover(ctx, Config{Mode: ModeAttach, Server: server.URL}, testDeps(server.Client()))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDiscoverAttachedServerUsesEnvironmentBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "veto" || password != "server-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"1.18.5"}`))
		case "/provider":
			_, _ = w.Write([]byte(`{"all":[],"connected":[],"default":{}}`))
		}
	}))
	defer server.Close()
	deps := testDeps(server.Client())
	deps.Getenv = func(name string) string {
		if name == "OPENCODE_SERVER_USERNAME" {
			return "veto"
		}
		if name == "OPENCODE_SERVER_PASSWORD" {
			return "server-secret"
		}
		return ""
	}
	_, err := Discover(context.Background(), Config{Mode: ModeAttach, Server: server.URL}, deps)
	assert.NoError(t, err)
}

func TestDiscoverCLIRunsSubprocessFake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	path := filepath.Join(t.TempDir(), "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '1.18.5\\n'; else printf 'openai/gpt-5\\n'; fi\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0755))
	deps := DefaultDependencies()
	deps.LookPath = func(string) (string, error) { return path, nil }

	result, err := Discover(context.Background(), Config{Mode: ModeCLI}, deps)
	require.NoError(t, err)
	assert.Equal(t, "1.18.5", result.Version)
	assert.Equal(t, []Model{{Provider: "openai", ID: "gpt-5"}}, result.Models)
}

func TestDefaultHTTPClientRejectsRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://example.com/steal")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	_, err := Discover(context.Background(), Config{Mode: ModeAttach, Server: server.URL}, DefaultDependencies())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirects are not allowed")
}

func TestDiscoverRejectsOversizedServerResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer server.Close()
	_, err := Discover(context.Background(), Config{Mode: ModeAttach, Server: server.URL}, testDeps(server.Client()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "size limit")
}

func TestParseModelsRejectsMalformedAndOversizedOutput(t *testing.T) {
	for _, output := range []string{"provideronly\n", "/model\n", "provider/\n", "provider/mo\x00del\n"} {
		_, err := parseCLIModels([]byte(output))
		assert.Error(t, err, output)
	}
	_, err := parseCLIModels([]byte(strings.Repeat("p/m\n", maxModels+1)))
	assert.Error(t, err)
}

func TestManagedDiscoveryStartsExplicitLoopbackServerAndClosesIt(t *testing.T) {
	proc := &fakeProcess{wait: make(chan error, 1)}
	var started []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"1.18.5"}`))
		case "/provider":
			_, _ = w.Write([]byte(`{"all":[],"connected":[],"default":{}}`))
		}
	}))
	defer server.Close()
	deps := testDeps(server.Client())
	deps.LookPath = func(string) (string, error) { return "/opt/opencode", nil }
	deps.Start = func(_ string, args []string, env []string) (Process, error) {
		started = append([]string{}, args...)
		assert.Contains(t, strings.Join(env, "\n"), "OPENCODE_SERVER_PASSWORD=")
		return proc, nil
	}

	connection, err := Connect(context.Background(), Config{Mode: ModeManaged, Server: loopbackURL(t, server.URL)}, deps)
	require.NoError(t, err)
	assert.Equal(t, []string{"serve", "--hostname", "127.0.0.1", "--port", strings.TrimPrefix(strings.Split(server.URL, ":")[2], "//")}, started)
	require.NoError(t, connection.Close())
	assert.True(t, proc.killed)
}

func TestManagedDiscoveryReportsPortConflict(t *testing.T) {
	proc := &fakeProcess{wait: make(chan error, 1)}
	proc.wait <- errors.New("address already in use")
	deps := Dependencies{
		LookPath: func(string) (string, error) { return "/opt/opencode", nil },
		Start:    func(string, []string, []string) (Process, error) { return proc, nil },
		Do: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		},
	}
	_, err := Connect(context.Background(), Config{Mode: ModeManaged, Server: "http://127.0.0.1:4096"}, deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address already in use")
}

func testDeps(client *http.Client) Dependencies {
	return Dependencies{Do: client.Do}
}

func loopbackURL(t *testing.T, raw string) string {
	t.Helper()
	return raw
}

type fakeProcess struct {
	wait   chan error
	killed bool
}

func (p *fakeProcess) Wait() error { return <-p.wait }
func (p *fakeProcess) Kill() error {
	p.killed = true
	select {
	case p.wait <- nil:
	default:
	}
	return nil
}
