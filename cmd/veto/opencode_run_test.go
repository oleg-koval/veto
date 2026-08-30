package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeRunProcess(t *testing.T) {
	if os.Getenv("VETO_TEST_OPENCODE_RUN") != "1" {
		return
	}
	cmdRun([]string{"--quiet", "--no-feedback", "--kind", "plan", "Create a short plan"})
}

func TestRunRoutesAdmissionAndExecutionThroughOpenCodeCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	home := t.TempDir()
	bin := t.TempDir()
	argsLog := filepath.Join(home, "opencode-args.log")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".veto"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".veto", "config.json"), []byte(`{"opencode":{"mode":"cli"}}`), 0600))
	script := `#!/bin/sh
case "$1" in
  --version) echo 1.18.5; exit 0 ;;
  models) echo openai/gpt-4.1; exit 0 ;;
  run)
    for arg in "$@"; do
      [ "$arg" = "--" ] && break
      printf '%s ' "$arg" >> "$HOME/opencode-args.log"
    done
    printf '\n' >> "$HOME/opencode-args.log"
    case "$*" in
      *veto:admission:*)
        printf '%s\n' '{"type":"text","sessionID":"ses_admission","part":{"id":"prt_admission","text":"{\"accept\":true,\"confidence\":0.99,\"reason_codes\":[],\"estimated_tokens\":50,\"estimated_cost_usd\":0.001,\"suggested_alternative_model\":\"\",\"required_task_changes\":[]}"}}'
        ;;
      *veto:execution:*)
        printf '%s\n' '{"type":"text","sessionID":"ses_execution","part":{"id":"prt_text","text":"final answer"}}'
        printf '%s\n' '{"type":"step_finish","sessionID":"ses_execution","part":{"reason":"stop","cost":0.002,"tokens":{"total":12,"input":8,"output":4}}}'
        ;;
      *) exit 3 ;;
    esac
    ;;
  *) exit 2 ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0700))

	command := exec.Command(os.Args[0], "-test.run=^TestOpenCodeRunProcess$")
	command.Env = []string{
		"VETO_TEST_OPENCODE_RUN=1",
		"HOME=" + home,
		"PATH=" + bin,
	}
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	assert.True(t, strings.HasPrefix(string(output), "final answer\n"), string(output))

	args, err := os.ReadFile(argsLog)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "--title veto:admission:")
	assert.Contains(t, lines[1], "--title veto:execution:")
	for _, line := range lines {
		assert.NotContains(t, " "+line+" ", " --auto ")
		assert.NotContains(t, " "+line+" ", " --yolo ")
		assert.NotContains(t, line, "dangerously-skip-permissions")
	}

	logs, err := filepath.Glob(filepath.Join(home, ".veto", "logs", "veto-*.log"))
	require.NoError(t, err)
	require.Len(t, logs, 1)
	file, err := os.Open(logs[0])
	require.NoError(t, err)
	defer file.Close()
	events, corrupt, err := ledger.Read(file)
	require.NoError(t, err)
	assert.Zero(t, corrupt)
	var sawAdmission, sawExecution bool
	for _, event := range events {
		if event.Type == ledger.EventAdmissionAccepted && event.Model == "opencode:openai/gpt-4.1" {
			sawAdmission = true
		}
		if event.Type == ledger.EventExecutionCompleted && event.Runtime == "opencode" {
			sawExecution = true
			require.NotNil(t, event.CostUSD)
			assert.InDelta(t, 0.002, *event.CostUSD, 0.000001)
		}
	}
	assert.True(t, sawAdmission)
	assert.True(t, sawExecution)
}
