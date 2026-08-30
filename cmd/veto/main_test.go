package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
)

type textOnlyTestExecutor struct{}

func (textOnlyTestExecutor) Run(context.Context, string) executor.Result { return executor.Result{} }
func (textOnlyTestExecutor) Execute(context.Context, string, executor.ExecutionOptions) executor.Result {
	return executor.Result{}
}
func (textOnlyTestExecutor) RuntimeID() string        { return "test-text" }
func (textOnlyTestExecutor) EffectiveTools() []string { return nil }

type toolTestExecutor struct{ textOnlyTestExecutor }

func (toolTestExecutor) EffectiveTools() []string { return []string{"read"} }

func TestProviderRegistryModelCapsUseEffectiveTransportTools(t *testing.T) {
	reg := &providerRegistry{
		executors: map[string]executor.RuntimeAdapter{
			"api": textOnlyTestExecutor{},
			"cli": toolTestExecutor{},
		},
		caps: map[string]router.ModelCapabilities{
			"api":     {Name: "api", Source: "builtin", Provider: "openai", APIModel: "model", SupportsTools: []string{"bash", "read"}},
			"cli":     {Name: "cli", SupportsTools: []string{"bash", "read"}},
			"missing": {Name: "missing", SupportsTools: []string{"bash", "read"}},
		},
	}

	got := map[string]router.ModelCapabilities{}
	for _, cap := range reg.modelCaps() {
		got[cap.Name] = cap
	}
	assert.Empty(t, got["api"].SupportsTools)
	assert.NotNil(t, got["api"].SupportsTools)
	assert.Equal(t, []string{"read"}, got["cli"].SupportsTools)
	assert.Empty(t, got["missing"].SupportsTools)
	assert.Equal(t, "test-text", got["api"].Runtime)
	assert.Equal(t, "test-text", got["cli"].Runtime)
	assert.Equal(t, router.ModelIdentity{
		Source: "builtin", Provider: "openai", Model: "model", Runtime: "test-text",
	}, got["api"].Identity())
}

func TestProviderRegistryModelCapsFiltersExactRuntime(t *testing.T) {
	reg := &providerRegistry{
		executors: map[string]executor.RuntimeAdapter{"direct": textOnlyTestExecutor{}, "open": runtimeTestExecutor{id: "opencode"}},
		caps:      map[string]router.ModelCapabilities{"direct": {Name: "direct"}, "open": {Name: "open"}},
	}

	got := reg.modelCapsForRuntime("opencode")
	if assert.Len(t, got, 1) {
		assert.Equal(t, "open", got[0].Name)
		assert.Equal(t, "opencode", got[0].Runtime)
	}
}

func TestProviderRegistryModelCapsFiltersProviderForHermesPin(t *testing.T) {
	reg := &providerRegistry{
		executors: map[string]executor.RuntimeAdapter{
			"openai": textOnlyTestExecutor{},
			"anthropic": textOnlyTestExecutor{},
		},
		caps: map[string]router.ModelCapabilities{
			"openai": {Name: "openai", Provider: "openai"},
			"anthropic": {Name: "anthropic", Provider: "anthropic"},
		},
	}
	got := reg.modelCapsForRuntimeProvider("", "OPENAI")
	if assert.Len(t, got, 1) {
		assert.Equal(t, "openai", got[0].Name)
	}
}

type runtimeTestExecutor struct{ id string }

func (r runtimeTestExecutor) Run(context.Context, string) executor.Result { return executor.Result{} }
func (r runtimeTestExecutor) Execute(context.Context, string, executor.ExecutionOptions) executor.Result {
	return executor.Result{}
}
func (r runtimeTestExecutor) RuntimeID() string      { return r.id }
func (runtimeTestExecutor) EffectiveTools() []string { return nil }

func TestRootHelpProcess(t *testing.T) {
	if arg := os.Getenv("VETO_TEST_ROOT_HELP"); arg != "" {
		os.Args = []string{"veto", arg}
		main()
		return
	}

	for _, arg := range []string{"help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRootHelpProcess$")
			cmd.Env = cleanRootHelpTestEnv(os.Environ())
			cmd.Env = append(cmd.Env,
				"VETO_TEST_ROOT_HELP="+arg,
				"HOME="+t.TempDir(),
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("root help exited with error: %v\n%s", err, output)
			}
			usage := string(output)
			for _, want := range []string{"USAGE", "COMMANDS"} {
				if !strings.Contains(usage, want) {
					t.Fatalf("root help output does not contain %q:\n%s", want, usage)
				}
			}
		})
	}
}

func cleanRootHelpTestEnv(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, value := range env {
		if strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "VETO_TEST_ROOT_HELP=") {
			continue
		}
		clean = append(clean, value)
	}
	return clean
}

func TestRootHelpRequested(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{arg: "help", want: true},
		{arg: "--help", want: true},
		{arg: "-h", want: true},
		{arg: "version", want: false},
		{arg: "route", want: false},
		{arg: "--helpful", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			if got := rootHelpRequested(tt.arg); got != tt.want {
				t.Fatalf("rootHelpRequested(%q) = %t, want %t", tt.arg, got, tt.want)
			}
		})
	}
}

func TestEffectiveVersion(t *testing.T) {
	tests := []struct {
		name          string
		linkedVersion string
		moduleVersion string
		want          string
	}{
		{name: "release binary", linkedVersion: "0.2.0", moduleVersion: "(devel)", want: "0.2.0"},
		{name: "linked tag", linkedVersion: "v0.2.0", moduleVersion: "v0.1.0", want: "0.2.0"},
		{name: "go install", linkedVersion: "dev", moduleVersion: "v0.1.0", want: "0.1.0"},
		{name: "local build", linkedVersion: "dev", moduleVersion: "(devel)", want: "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, effectiveVersion(tt.linkedVersion, tt.moduleVersion))
		})
	}
}

func TestPrintUsageContainsRootHelpContent(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)
	usage := out.String()
	for _, want := range []string{"USAGE", "COMMANDS", "QUICK START", "PROVIDERS", "doctor", "hermes", "models"} {
		if !strings.Contains(usage, want) {
			t.Fatalf("usage does not contain %q", want)
		}
	}
	assert.Contains(t, usage, "OPENROUTER_API_KEY    1 built-in model plus the dynamic catalog: meta-llama/llama-4-maverick")
	assert.NotContains(t, usage, "100+ more")
}

func TestProvidersReportsOpenRouterRoutableCount(t *testing.T) {
	if os.Getenv("VETO_TEST_OPENROUTER_PROVIDERS") == "1" {
		cmdProviders()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestProvidersReportsOpenRouterRoutableCount$")
	cmd.Env = []string{
		"HOME=" + t.TempDir(),
		"OPENROUTER_API_KEY=test-key",
		"VETO_TEST_OPENROUTER_PROVIDERS=1",
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("providers exited with error: %v\n%s", err, output)
	}

	providers := string(output)
	assert.Contains(t, providers, "1 built-in model plus the dynamic catalog: meta-llama/llama-4-maverick")
	assert.Contains(t, providers, "1 model(s) available for routing")
	assert.NotContains(t, providers, "100+")
}
