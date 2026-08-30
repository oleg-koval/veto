// Package opencode discovers models exposed by a local OpenCode runtime.
package opencode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxResponseBytes = 2 << 20
	maxCommandBytes  = 2 << 20
	maxModels        = 5000
	managedWait      = 5 * time.Second
	shutdownWait     = 2 * time.Second
)

// Mode selects how Veto reaches OpenCode.
type Mode string

const (
	ModeAttach  Mode = "attach"
	ModeManaged Mode = "managed"
	ModeCLI     Mode = "cli"
)

// Config is safe to persist in ~/.veto/config.json. It never contains provider
// credentials or OpenCode's provider-auth state.
type Config struct {
	Mode   Mode   `json:"mode"`
	Server string `json:"server,omitempty"`
}

// Model is an OpenCode provider/model pair.
type Model struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// Discovery describes a reachable OpenCode runtime and its connected models.
type Discovery struct {
	Mode       Mode    `json:"mode"`
	Server     string  `json:"server,omitempty"`
	Executable string  `json:"executable,omitempty"`
	Version    string  `json:"version"`
	Models     []Model `json:"models"`
}

// IncompatibleError means OpenCode was reachable but did not satisfy the
// documented CLI or server contract.
type IncompatibleError struct {
	Version string
	Detail  string
}

func (e *IncompatibleError) Error() string {
	version := e.Version
	if version == "" {
		version = "unknown version"
	}
	return fmt.Sprintf("OpenCode %s is incompatible: %s", version, e.Detail)
}

// Process is the minimal managed-server lifecycle used by discovery.
type Process interface {
	Wait() error
	Kill() error
}

// Dependencies makes filesystem-free process and HTTP boundaries injectable.
type Dependencies struct {
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) ([]byte, error)
	Start    func(string, []string, []string) (Process, error)
	Do       func(*http.Request) (*http.Response, error)
	Getenv   func(string) string
}

// Connection owns a managed OpenCode process when Mode is managed.
type Connection struct {
	Discovery Discovery
	process   Process
	wait      <-chan error
}

// Close releases a managed server. Attach and CLI connections are no-ops.
func (c *Connection) Close() error {
	if c == nil || c.process == nil {
		return nil
	}
	err := c.process.Kill()
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if c.wait != nil {
		select {
		case <-c.wait:
		case <-time.After(shutdownWait):
			return errors.New("timed out waiting for the managed OpenCode server to stop")
		}
	}
	c.process = nil
	return nil
}

// DefaultDependencies returns bounded production process and HTTP boundaries.
func DefaultDependencies() Dependencies {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("OpenCode server redirects are not allowed")
		},
	}
	return Dependencies{
		LookPath: exec.LookPath,
		Run:      runCommand,
		Start:    startProcess,
		Do:       client.Do,
		Getenv:   os.Getenv,
	}
}

// Discover connects, returns metadata, and closes any temporary managed server.
func Discover(ctx context.Context, config Config, deps Dependencies) (Discovery, error) {
	connection, err := Connect(ctx, config, deps)
	if err != nil {
		return Discovery{}, err
	}
	discovery := connection.Discovery
	if err := connection.Close(); err != nil {
		return Discovery{}, fmt.Errorf("stop temporary managed OpenCode server: %w", err)
	}
	return discovery, nil
}

// ProbeCLI checks that the CLI exists and reports a compatible version without
// listing models or contacting providers. It is suitable for doctor checks of
// managed mode, where starting a server would be an unwanted side effect.
func ProbeCLI(ctx context.Context, deps Dependencies) (Discovery, error) {
	deps = fillDependencies(deps)
	path, version, err := cliVersion(ctx, deps)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{Mode: ModeCLI, Executable: path, Version: version, Models: []Model{}}, nil
}

// Connect discovers OpenCode and retains a managed process until Close.
func Connect(ctx context.Context, config Config, deps Dependencies) (*Connection, error) {
	deps = fillDependencies(deps)
	switch config.Mode {
	case ModeAttach:
		server, err := ValidateServerURL(config.Server)
		if err != nil {
			return nil, err
		}
		discovery, err := discoverServer(ctx, config.Mode, server, deps, deps.Getenv("OPENCODE_SERVER_USERNAME"), deps.Getenv("OPENCODE_SERVER_PASSWORD"))
		if err != nil {
			return nil, err
		}
		return &Connection{Discovery: discovery}, nil
	case ModeManaged:
		return connectManaged(ctx, config, deps)
	case ModeCLI:
		discovery, err := discoverCLI(ctx, deps)
		if err != nil {
			return nil, err
		}
		return &Connection{Discovery: discovery}, nil
	default:
		return nil, fmt.Errorf("unsupported OpenCode mode %q", config.Mode)
	}
}

// ValidateServerURL accepts only explicit local HTTP endpoints. It normalizes
// localhost to 127.0.0.1 to avoid name-resolution surprises.
func ValidateServerURL(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid OpenCode server URL: %w", err)
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("OpenCode server must be a plain HTTP loopback URL without credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("OpenCode server URL must not include a path")
	}
	if parsed.Port() == "" {
		return "", errors.New("OpenCode server URL must include an explicit port")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("OpenCode server URL has an invalid port")
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "localhost":
		host = "127.0.0.1"
	case "127.0.0.1", "::1":
	default:
		return "", errors.New("OpenCode server must use localhost, 127.0.0.1, or ::1")
	}
	if host == "::1" {
		return "http://[::1]:" + strconv.Itoa(port), nil
	}
	return "http://" + host + ":" + strconv.Itoa(port), nil
}

func fillDependencies(deps Dependencies) Dependencies {
	defaults := DefaultDependencies()
	if deps.LookPath == nil {
		deps.LookPath = defaults.LookPath
	}
	if deps.Run == nil {
		deps.Run = defaults.Run
	}
	if deps.Start == nil {
		deps.Start = defaults.Start
	}
	if deps.Do == nil {
		deps.Do = defaults.Do
	}
	if deps.Getenv == nil {
		deps.Getenv = defaults.Getenv
	}
	return deps
}

func discoverCLI(ctx context.Context, deps Dependencies) (Discovery, error) {
	path, version, err := cliVersion(ctx, deps)
	if err != nil {
		return Discovery{}, err
	}
	output, err := deps.Run(ctx, path, "models")
	if err != nil {
		return Discovery{}, fmt.Errorf("list OpenCode models: %w", err)
	}
	models, err := parseCLIModels(output)
	if err != nil {
		return Discovery{}, &IncompatibleError{Version: version, Detail: err.Error()}
	}
	return Discovery{Mode: ModeCLI, Executable: path, Version: version, Models: models}, nil
}

func cliVersion(ctx context.Context, deps Dependencies) (string, string, error) {
	path, err := deps.LookPath("opencode")
	if err != nil {
		return "", "", fmt.Errorf("OpenCode CLI is not available on PATH: %w", err)
	}
	versionOutput, err := deps.Run(ctx, path, "--version")
	if err != nil {
		return "", "", fmt.Errorf("check OpenCode CLI version: %w", err)
	}
	version := strings.TrimSpace(string(versionOutput))
	if !semanticVersion.MatchString(version) {
		return "", "", &IncompatibleError{Version: version, Detail: "expected a semantic version from 'opencode --version'"}
	}
	return path, version, nil
}

var semanticVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func parseCLIModels(output []byte) ([]Model, error) {
	if len(output) > maxCommandBytes {
		return nil, errors.New("'opencode models' output exceeds the size limit")
	}
	if !utf8.Valid(output) {
		return nil, errors.New("'opencode models' returned invalid UTF-8")
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []Model{}, nil
	}
	if len(lines) > maxModels {
		return nil, fmt.Errorf("'opencode models' returned more than %d models", maxModels)
	}
	seen := make(map[string]bool, len(lines))
	models := make([]Model, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		provider, model, ok := strings.Cut(line, "/")
		if !ok || !validIdentifier(provider) || !validIdentifier(model) {
			return nil, fmt.Errorf("'opencode models' returned an invalid provider/model line")
		}
		key := provider + "\x00" + model
		if !seen[key] {
			seen[key] = true
			models = append(models, Model{Provider: provider, ID: model})
		}
	}
	sortModels(models)
	return models, nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func discoverServer(ctx context.Context, mode Mode, server string, deps Dependencies, username, password string) (Discovery, error) {
	healthBody, status, err := get(ctx, deps, server+"/global/health", username, password)
	if err != nil {
		return Discovery{}, fmt.Errorf("contact OpenCode server: %w", err)
	}
	if status != http.StatusOK {
		return Discovery{}, fmt.Errorf("OpenCode health endpoint returned HTTP %d", status)
	}
	var health struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if !utf8.Valid(healthBody) || json.Unmarshal(healthBody, &health) != nil {
		return Discovery{}, &IncompatibleError{Detail: "health response does not match the documented API"}
	}
	health.Version = strings.TrimSpace(health.Version)
	if !health.Healthy || !semanticVersion.MatchString(health.Version) {
		return Discovery{}, &IncompatibleError{Version: health.Version, Detail: "health response does not match the documented API"}
	}
	models, err := discoverServerModels(ctx, server, deps, username, password, health.Version)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{Mode: mode, Server: server, Version: health.Version, Models: models}, nil
}

func discoverServerModels(ctx context.Context, server string, deps Dependencies, username, password, version string) ([]Model, error) {
	body, status, err := get(ctx, deps, server+"/provider", username, password)
	if err != nil {
		return nil, fmt.Errorf("list OpenCode providers: %w", err)
	}
	if status == http.StatusOK {
		models, parseErr := parseCurrentProviders(body)
		if parseErr != nil {
			return nil, &IncompatibleError{Version: version, Detail: parseErr.Error()}
		}
		return models, nil
	}
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return nil, fmt.Errorf("OpenCode provider endpoint returned HTTP %d", status)
	}
	body, status, err = get(ctx, deps, server+"/config/providers", username, password)
	if err != nil {
		return nil, fmt.Errorf("list OpenCode providers: %w", err)
	}
	if status != http.StatusOK {
		return nil, &IncompatibleError{Version: version, Detail: "neither /provider nor /config/providers is available"}
	}
	models, err := parseLegacyProviders(body)
	if err != nil {
		return nil, &IncompatibleError{Version: version, Detail: err.Error()}
	}
	return models, nil
}

type apiProvider struct {
	ID     string                     `json:"id"`
	Models map[string]json.RawMessage `json:"models"`
}

func parseCurrentProviders(body []byte) ([]Model, error) {
	var response struct {
		All       []apiProvider `json:"all"`
		Connected *[]string     `json:"connected"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &response) != nil || response.All == nil || response.Connected == nil {
		return nil, errors.New("provider response does not match the documented API")
	}
	connected := make(map[string]bool, len(*response.Connected))
	for _, id := range *response.Connected {
		if !validIdentifier(id) {
			return nil, errors.New("provider response contains an invalid connected provider")
		}
		connected[id] = true
	}
	return modelsFromProviders(response.All, connected)
}

func parseLegacyProviders(body []byte) ([]Model, error) {
	var response struct {
		Providers []apiProvider `json:"providers"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &response) != nil || response.Providers == nil {
		return nil, errors.New("config/providers response does not match the documented API")
	}
	return modelsFromProviders(response.Providers, nil)
}

func modelsFromProviders(providers []apiProvider, connected map[string]bool) ([]Model, error) {
	models := make([]Model, 0)
	seen := make(map[string]bool)
	for _, provider := range providers {
		if !validIdentifier(provider.ID) || provider.Models == nil {
			return nil, errors.New("provider response contains an invalid provider")
		}
		if connected != nil && !connected[provider.ID] {
			continue
		}
		for key, raw := range provider.Models {
			var value *struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return nil, errors.New("provider response contains an invalid model")
			}
			if value.ID == "" {
				value.ID = key
			}
			if !validIdentifier(value.ID) {
				return nil, errors.New("provider response contains an invalid model")
			}
			identity := provider.ID + "\x00" + value.ID
			if !seen[identity] {
				seen[identity] = true
				models = append(models, Model{Provider: provider.ID, ID: value.ID})
			}
			if len(models) > maxModels {
				return nil, fmt.Errorf("OpenCode returned more than %d models", maxModels)
			}
		}
	}
	sortModels(models)
	return models, nil
}

func sortModels(models []Model) {
	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider == models[j].Provider {
			return models[i].ID < models[j].ID
		}
		return models[i].Provider < models[j].Provider
	})
}

func get(ctx context.Context, deps Dependencies, target, username, password string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	if password != "" {
		if username == "" {
			username = "opencode"
		}
		request.SetBasicAuth(username, password)
	}
	response, err := deps.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(body) > maxResponseBytes {
		return nil, response.StatusCode, errors.New("OpenCode response exceeds the size limit")
	}
	return body, response.StatusCode, nil
}

func connectManaged(ctx context.Context, config Config, deps Dependencies) (*Connection, error) {
	server := config.Server
	if strings.TrimSpace(server) == "" {
		server = "http://127.0.0.1:4096"
	}
	server, err := ValidateServerURL(server)
	if err != nil {
		return nil, err
	}
	path, err := deps.LookPath("opencode")
	if err != nil {
		return nil, fmt.Errorf("OpenCode CLI is required for managed mode: %w", err)
	}
	parsed, _ := url.Parse(server)
	password, err := randomPassword()
	if err != nil {
		return nil, fmt.Errorf("secure managed OpenCode server: %w", err)
	}
	args := []string{"serve", "--hostname", parsed.Hostname(), "--port", parsed.Port()}
	process, err := deps.Start(path, args, []string{
		"OPENCODE_SERVER_USERNAME=opencode",
		"OPENCODE_SERVER_PASSWORD=" + password,
	})
	if err != nil {
		return nil, fmt.Errorf("start managed OpenCode server: %w", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- process.Wait() }()
	connection := &Connection{process: process, wait: wait}
	startCtx, cancel := context.WithTimeout(ctx, managedWait)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	readyOnce := false
	for {
		discovery, probeErr := discoverServer(startCtx, ModeManaged, server, deps, "opencode", password)
		if probeErr == nil && readyOnce {
			connection.Discovery = discovery
			return connection, nil
		}
		if probeErr == nil {
			readyOnce = true
		} else {
			readyOnce = false
		}
		lastErr = probeErr
		select {
		case processErr := <-wait:
			connection.wait = nil
			connection.process = nil
			if processErr == nil {
				processErr = errors.New("process exited before becoming ready")
			}
			return nil, fmt.Errorf("managed OpenCode server stopped before becoming ready (check that %s is free): %w", parsed.Host, processErr)
		case <-startCtx.Done():
			_ = connection.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("managed OpenCode server did not become ready: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func randomPassword() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func runCommand(ctx context.Context, path string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, path, args...)
	var output limitedBuffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if output.overflow {
		return nil, errors.New("OpenCode command output exceeds the size limit")
	}
	if err != nil {
		message := strings.TrimSpace(output.buffer.String())
		if message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}
	return output.buffer.Bytes(), nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	remaining := maxCommandBytes - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:min(remaining, len(p))])
	}
	if len(p) > remaining {
		w.overflow = true
	}
	return len(p), nil
}

func startProcess(path string, args []string, env []string) (Process, error) {
	command := exec.Command(path, args...)
	command.Env = mergeEnvironment(os.Environ(), env)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &osProcess{command: command}, nil
}

func mergeEnvironment(base, overrides []string) []string {
	replaced := make(map[string]bool, len(overrides))
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			replaced[key] = true
		}
	}
	merged := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !replaced[key] {
			merged = append(merged, entry)
		}
	}
	return append(merged, overrides...)
}

type osProcess struct{ command *exec.Cmd }

func (p *osProcess) Wait() error { return p.command.Wait() }
func (p *osProcess) Kill() error { return p.command.Process.Kill() }
