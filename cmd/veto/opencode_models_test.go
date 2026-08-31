package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/opencode"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddOpenCodeModelsPreservesKnownMetadataAndUnknowns(t *testing.T) {
	reg := &providerRegistry{
		executors: map[string]execution.RuntimeAdapter{"gpt-5": textOnlyTestExecutor{}},
		caps: map[string]router.ModelCapabilities{
			"gpt-5": {
				Name: "gpt-5", Provider: "openai", APIModel: "gpt-5", Tier: "large", MaxContextTokens: 100,
				CostPer1kInputUSD: 0.01, CostPer1kOutputUSD: 0.02,
			},
		},
	}
	discovery := opencode.Discovery{Mode: opencode.ModeCLI, Executable: "/safe/opencode", Models: []opencode.Model{
		{Provider: "openai", ID: "gpt-5"},
		{Provider: "custom", ID: "private-model"},
	}}

	addOpenCodeModels(reg, opencode.Config{Mode: opencode.ModeCLI}, discovery, opencode.Dependencies{}, nil)

	known := reg.caps["opencode:openai/gpt-5"]
	assert.Equal(t, "opencode", known.Source)
	assert.Equal(t, "large", known.Tier)
	assert.False(t, known.CostPer1kInputUnknown)
	unknown := reg.caps["opencode:custom/private-model"]
	assert.Empty(t, unknown.Tier)
	assert.True(t, unknown.CostPer1kInputUnknown)
	assert.True(t, unknown.CostPer1kOutputUnknown)
	assert.Nil(t, unknown.SupportsTools)
	assert.Equal(t, "opencode", reg.executors[unknown.Name].RuntimeID())
	for _, capability := range reg.modelCaps() {
		if capability.Name == unknown.Name {
			assert.Nil(t, capability.SupportsTools, "undiscovered OpenCode tools must remain unknown")
		}
	}
}

func TestAddOpenCodeModelsHonorsDisabledBinding(t *testing.T) {
	reg := &providerRegistry{executors: map[string]execution.RuntimeAdapter{}, caps: map[string]router.ModelCapabilities{}}
	model := opencode.Model{Provider: "openai", ID: "gpt-5"}
	addOpenCodeModels(reg, opencode.Config{Mode: opencode.ModeCLI}, opencode.Discovery{Models: []opencode.Model{model}}, opencode.Dependencies{}, map[string]bool{
		openCodeModelName(model): true,
	})
	assert.Empty(t, reg.caps)
}

func TestBuildProviderRegistryLoadsConnectedOpenCodeModels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell subprocess fixture")
	}
	home := t.TempDir()
	bin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "XAI_API_KEY", "CLAUDE_SUBSCRIPTION"} {
		t.Setenv(key, "")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".veto"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".veto", "config.json"), []byte(`{"opencode":{"mode":"cli"}}`), 0600))
	script := "#!/bin/sh\ncase \"$1\" in\n  --version) echo 1.18.5 ;;\n  models) echo openai/gpt-5 ;;\n  *) exit 2 ;;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0700))

	reg, err := buildProviderRegistry()
	require.NoError(t, err)
	exec, ok := reg.executors["opencode:openai/gpt-5"]
	require.True(t, ok)
	assert.Equal(t, "opencode", exec.RuntimeID())
	assert.Equal(t, "openai", reg.caps["opencode:openai/gpt-5"].Provider)
}
