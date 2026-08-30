package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/opencode"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reportedAgenticObjective = "fix and resolve all codex comments in this pr, push when you done https://github.com/oleg-koval/roazon/pull/1513"

func TestAgenticRunTimeoutDefaults(t *testing.T) {
	assert.GreaterOrEqual(t, defaultRunTimeout, 45*time.Minute)
	assert.GreaterOrEqual(t, defaultAdmissionTimeout, 45*time.Second)
	assert.Less(t, defaultAdmissionTimeout, defaultRunTimeout)
}

func TestExecutionPromptRequiresLivePRThreadAudit(t *testing.T) {
	prompt := executionPrompt(reportedAgenticObjective, []string{"- follow repository conventions"})

	assert.Contains(t, prompt, "reviewThreads(first:100)")
	assert.Contains(t, prompt, "zero unresolved matching threads")
	assert.Contains(t, prompt, "reply to and resolve")
	assert.Contains(t, prompt, "commit and push")
}

func TestExecutionPromptLeavesOrdinaryTasksUnchanged(t *testing.T) {
	assert.Equal(t, "summarize the release", executionPrompt("summarize the release", nil))
}

func TestClaudeAgenticRunProcess(t *testing.T) {
	if os.Getenv("VETO_TEST_CLAUDE_AGENTIC_RUN") != "1" {
		return
	}
	cmdRun([]string{"--quiet", "--no-feedback", "--kind", "code-change", reportedAgenticObjective})
}

func TestRunExactAgenticObjectiveUsesStructuredAdmissionThenExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	home := t.TempDir()
	bin := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".veto"), 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(home, ".veto", "credentials.json"),
		[]byte(`{"CLAUDE_SUBSCRIPTION":"true"}`),
		0600,
	))

	script := `#!/bin/sh
case "$*" in
  *"--output-format json"*)
	case "$*" in
	  *"--safe-mode"*"--json-schema"*) ;;
	  *) exit 4 ;;
	esac
	printf '%s\n' 'admission' >> "$HOME/claude-calls.log"
    printf '%s\n' '{"is_error":false,"result":"fallback","structured_output":{"accept":true,"confidence":0.95,"reason_codes":[],"estimated_tokens":100,"estimated_cost_usd":0,"suggested_alternative_model":"","required_task_changes":[]}}'
    ;;
  *"--output-format text"*)
	case "$*" in
	  *"reviewThreads(first:100)"*"zero unresolved matching threads"*) ;;
	  *) exit 5 ;;
	esac
	printf '%s\n' 'execution' >> "$HOME/claude-calls.log"
    printf '%s\n' 'agentic execution complete'
    ;;
  *) exit 2 ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700))

	command := exec.Command(os.Args[0], "-test.run=^TestClaudeAgenticRunProcess$")
	command.Env = append(cleanRunTestEnv(os.Environ()),
		"VETO_TEST_CLAUDE_AGENTIC_RUN=1",
		"HOME="+home,
		"PATH="+bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	assert.Contains(t, string(output), "agentic execution complete")

	calls, err := os.ReadFile(filepath.Join(home, "claude-calls.log"))
	require.NoError(t, err)
	assert.Equal(t, "admission\nexecution\n", string(calls))
}

func cleanRunTestEnv(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, value := range env {
		if strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "PATH=") ||
			strings.HasPrefix(value, "VETO_TEST_CLAUDE_AGENTIC_RUN=") ||
			strings.HasPrefix(value, "ANTHROPIC_API_KEY=") ||
			strings.HasPrefix(value, "OPENAI_API_KEY=") ||
			strings.HasPrefix(value, "OPENROUTER_API_KEY=") ||
			strings.HasPrefix(value, "XAI_API_KEY=") ||
			strings.HasPrefix(value, "CLAUDE_SUBSCRIPTION=") {
			continue
		}
		clean = append(clean, value)
	}
	return clean
}

func TestValidateExecutionResultRejectsTruncation(t *testing.T) {
	err := validateExecutionResult(executor.Result{
		Output:       "partial output",
		FinishReason: "max_output_tokens",
		Truncated:    true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "truncated")
	assert.ErrorContains(t, err, "max_output_tokens")
	assert.ErrorContains(t, err, "--max-output-tokens")
}

func TestValidateExecutionResultAcceptsCompleteOutput(t *testing.T) {
	require.NoError(t, validateExecutionResult(executor.Result{Output: "complete output"}))
}

func TestValidateExecutionResultPreservesProviderError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	err := validateExecutionResult(executor.Result{Error: providerErr})

	assert.ErrorIs(t, err, providerErr)
}

func TestHasEffectiveToolsUsesRuntimeCapabilities(t *testing.T) {
	assert.False(t, hasEffectiveTools(executor.NewOpenAIExecutor("key", "gpt-4.1")))
	assert.True(t, hasEffectiveTools(executor.NewClaudeCLIExecutor("sonnet")))
	assert.False(t, isTextOnlyRuntime(opencode.NewRuntime(opencode.Config{}, opencode.Discovery{}, opencode.Model{}, opencode.Dependencies{})))
}

func TestExecutionMetricsPrefersProviderReportedCost(t *testing.T) {
	metrics := executionMetrics(router.ModelCapabilities{
		CostPer1kInputUnknown: true, CostPer1kOutputUnknown: true,
	}, executor.Result{
		Usage:   executor.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Known: true},
		CostUSD: 0.0125, CostKnown: true,
	}, time.Second, "success")

	assert.True(t, metrics.UsageKnown)
	assert.True(t, metrics.CostKnown)
	assert.InDelta(t, 0.0125, metrics.CostUSD, 0.000001)
}

func TestExecutionMetricsKeepsUnknownPriceUnknown(t *testing.T) {
	metrics := executionMetrics(router.ModelCapabilities{
		CostPer1kInputUnknown: true, CostPer1kOutputUnknown: true,
	}, executor.Result{
		Usage: executor.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Known: true},
	}, time.Second, "success")

	assert.True(t, metrics.UsageKnown)
	assert.False(t, metrics.CostKnown)
}

func TestWriteOutputFile(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(old) })

	require.NoError(t, writeOutputFile("result.md", "```md\nhello\n```", false))
	data, err := os.ReadFile(filepath.Join(dir, "result.md"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))

	info, err := os.Stat(filepath.Join(dir, "result.md"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	require.ErrorContains(t, writeOutputFile("result.md", "replacement", false), "already exists")
	require.NoError(t, writeOutputFile("result.md", "replacement", true))
	data, err = os.ReadFile(filepath.Join(dir, "result.md"))
	require.NoError(t, err)
	assert.Equal(t, "replacement\n", string(data))
}

func TestWriteOutputFileRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"../outside.txt", "/tmp/absolute.txt", ".env", "nested/.credentials"} {
		t.Run(path, func(t *testing.T) {
			require.Error(t, writeOutputFile(path, "secret", false))
		})
	}
}

func TestWriteOutputFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.txt")
	link := filepath.Join(dir, "result.txt")
	require.NoError(t, os.Symlink(target, link))
	old, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(old) })

	require.ErrorContains(t, writeOutputFile("result.txt", "secret", true), "symbolic links")
	_, err = os.Stat(target)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
