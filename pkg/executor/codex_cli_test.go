package executor

import (
	"bytes"
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
	case "$all" in
	  *"--json"*) ;;
	  *) exit 7 ;;
	esac
  printf '%s\n' execution >> "$VETO_CODEX_CALLS"
	printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
	printf '%s\n' '{"type":"item.started","item":{"id":"item-1","type":"command_execution","status":"in_progress"}}'
	printf '%s\n' '{"type":"item.completed","item":{"id":"item-1","type":"command_execution","status":"completed"}}'
	printf '%s\n' '{"type":"item.completed","item":{"id":"item-2","type":"agent_message","text":"working"}}'
	printf '%s\n' '{"type":"item.started","item":{"id":"item-3","type":"file_change","status":"in_progress"}}'
	printf '%s\n' '{"type":"item.completed","item":{"id":"item-3","type":"file_change","status":"completed"}}'
	printf '%s\n' '{"type":"item.completed","item":{"id":"item-4","type":"agent_message","text":"codex execution complete"}}'
	printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":21,"cached_input_tokens":8,"output_tokens":13,"reasoning_output_tokens":5}}'
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
	assert.Equal(t, Usage{InputTokens: 21, OutputTokens: 13, TotalTokens: 34, Known: true}, execution.Usage)
	assert.True(t, execution.CostKnown)
	assert.Zero(t, execution.CostUSD)
	assert.Equal(t, "codex-cli", exec.RuntimeID())
	assert.Equal(t, []string{"bash", "read", "write", "edit"}, exec.EffectiveTools())

	calls, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "admission\nexecution\n", string(calls))
}

func TestCodexCLIStreamsAgentMessagesAndSafeToolEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "codex")
	fixture := `#!/bin/sh
printf '%s\n' '{"type":"item.started","item":{"id":"item-1","type":"command_execution","command":"secret command","status":"in_progress"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item-1","type":"command_execution","command":"secret command","status":"completed"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item-2","type":"agent_message","text":"first update"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item-3","type":"agent_message","text":"final answer"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":2}}'
`
	require.NoError(t, os.WriteFile(script, []byte(fixture), 0700))

	exec := NewCodexCLIExecutor()
	exec.binary = script
	var output bytes.Buffer
	var events []RuntimeEvent
	result := exec.ExecuteWithEvents(t.Context(), "execute", ExecutionOptions{}, &output, func(event RuntimeEvent) {
		events = append(events, event)
	})

	require.NoError(t, result.Error)
	assert.Equal(t, "first update\nfinal answer\n", output.String())
	assert.Equal(t, "final answer", result.Output)
	assert.Equal(t, []RuntimeEvent{
		{Kind: RuntimeToolStarted, Name: "shell", Status: "running"},
		{Kind: RuntimeToolCompleted, Name: "shell", Status: "completed"},
	}, events)
}

func TestCodexCLIRejectsMalformedOrEmptyEventOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	for name, tc := range map[string]struct {
		event string
		want  string
	}{
		"malformed": {event: "not-json", want: "decode codex cli execution event"},
		"empty":     {event: `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":0}}`, want: "empty response"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "codex")
			require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' '"+tc.event+"'\n"), 0700))
			exec := NewCodexCLIExecutor()
			exec.binary = script
			result := exec.Execute(t.Context(), "execute", ExecutionOptions{})
			require.Error(t, result.Error)
			assert.Contains(t, result.Error.Error(), tc.want)
		})
	}
}
