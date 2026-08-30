package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionAdmissionSchemaFixesMarginalProviderCostAtZero(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Minimum *float64 `json:"minimum"`
			Maximum *float64 `json:"maximum"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(admissionJSONSchema), &schema))
	cost := schema.Properties["estimated_cost_usd"]
	require.NotNil(t, cost.Minimum)
	require.NotNil(t, cost.Maximum)
	assert.Zero(t, *cost.Minimum)
	assert.Zero(t, *cost.Maximum)
}

func TestCodexCLIUsesIsolatedStructuredAdmissionAndNormalExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := filepath.Join(dir, "codex")
	fixture := `#!/bin/sh
output=""
schema=""
all="$*"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message) shift; output="$1" ;;
    --output-schema) shift; schema="$1" ;;
  esac
  shift
done
if [ -n "$schema" ]; then
  test -s "$schema" || exit 4
  case "$all" in
    *"--ephemeral"*"--ignore-user-config"*"--ignore-rules"*"--sandbox read-only"*) ;;
    *) exit 5 ;;
  esac
  printf '%s\n' admission >> "$VETO_CODEX_CALLS"
  printf '%s\n' '{"accept":true,"confidence":0.9,"reason_codes":[],"estimated_tokens":100,"estimated_cost_usd":0,"suggested_alternative_model":"","required_task_changes":[]}' > "$output"
else
  case "$all" in
    *"--ignore-user-config"*|*"--ignore-rules"*|*"--sandbox read-only"*) exit 6 ;;
  esac
  printf '%s\n' execution >> "$VETO_CODEX_CALLS"
  printf '%s\n' 'codex execution complete'
fi
`
	require.NoError(t, os.WriteFile(script, []byte(fixture), 0700))
	t.Setenv("VETO_CODEX_CALLS", logPath)

	exec := NewCodexCLIExecutor()
	exec.binary = script
	admission := exec.Run(t.Context(), "admit")
	require.NoError(t, admission.Error)
	assert.Contains(t, admission.Output, `"accept":true`)
	execution := exec.Execute(t.Context(), "execute", ExecutionOptions{})
	require.NoError(t, execution.Error)
	assert.Equal(t, "codex execution complete", execution.Output)
	assert.Equal(t, "codex-cli", exec.RuntimeID())
	assert.Equal(t, []string{"bash", "read", "write", "edit"}, exec.EffectiveTools())

	calls, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "admission\nexecution\n", string(calls))
}
