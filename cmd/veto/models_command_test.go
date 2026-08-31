package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unknownToolsTestExecutor struct{ textOnlyTestExecutor }

func (unknownToolsTestExecutor) EffectiveToolsKnown() bool { return false }

func TestModelsJSONPreservesUnknownAndKnownZero(t *testing.T) {
	reg := &providerRegistry{
		executors: map[string]execution.RuntimeAdapter{
			"free":    textOnlyTestExecutor{},
			"unknown": unknownToolsTestExecutor{},
		},
		caps: map[string]router.ModelCapabilities{
			"free": {
				Name: "free", Source: "local-config", Provider: "local", APIModel: "free-api",
				SupportsTools: []string{}, CostPer1kInputUSD: 0, CostPer1kOutputUSD: 0,
			},
			"unknown": {
				Name: "unknown", Source: "opencode", Provider: "vendor", APIModel: "unknown-api",
				SupportsTools: nil, CostPer1kInputUnknown: true, CostPer1kOutputUnknown: true,
			},
		},
	}
	var stdout, stderr bytes.Buffer
	code := runModelsCommand([]string{"--json", "--offline"}, &stdout, &stderr, func(offline bool) (*providerRegistry, error) {
		assert.True(t, offline)
		return reg, nil
	})
	require.Equal(t, 0, code, stderr.String())
	var result modelListResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	require.Len(t, result.Models, 2)
	assert.Equal(t, "free", result.Models[0].Name)
	assert.True(t, result.Models[0].ToolsKnown)
	assert.True(t, result.Models[0].CostPer1kInputKnown)
	assert.Zero(t, result.Models[0].CostPer1kInputUSD)
	assert.False(t, result.Models[1].ToolsKnown)
	assert.False(t, result.Models[1].CostPer1kInputKnown)
}
