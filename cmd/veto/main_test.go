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

type toolTestExecutor struct{ textOnlyTestExecutor }

func (toolTestExecutor) EffectiveTools() []string { return []string{"read"} }

func TestProviderRegistryModelCapsUseEffectiveTransportTools(t *testing.T) {
	reg := &providerRegistry{
		executors: map[string]router.Executor{
			"api": textOnlyTestExecutor{},
			"cli": toolTestExecutor{},
		},
		caps: map[string]router.ModelCapabilities{
			"api": {Name: "api", SupportsTools: []string{"bash", "read"}},
			"cli": {Name: "cli", SupportsTools: []string{"bash", "read"}},
		},
	}

	got := map[string]router.ModelCapabilities{}
	for _, cap := range reg.modelCaps() {
		got[cap.Name] = cap
	}
	assert.Empty(t, got["api"].SupportsTools)
	assert.Equal(t, []string{"read"}, got["cli"].SupportsTools)
}

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
	for _, want := range []string{"USAGE", "COMMANDS", "QUICK START", "PROVIDERS"} {
		if !strings.Contains(usage, want) {
			t.Fatalf("usage does not contain %q", want)
		}
	}
}
