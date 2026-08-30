package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CLIExecutor runs a subscription CLI (e.g. claude -p) to answer prompts.
// Cost is $0 marginal — the user pays a flat subscription, not per token.
type CLIExecutor struct {
	binary string // "claude"
	model  string
}

const admissionJSONSchema = `{"type":"object","properties":{"accept":{"type":"boolean"},"confidence":{"type":"number","minimum":0,"maximum":1},"reason_codes":{"type":"array","items":{"type":"string","enum":["MISSING_REQUIRED_TOOL","CONTEXT_TOO_LARGE","COST_CEILING_EXCEEDED","TASK_KIND_OUTSIDE_STRENGTHS","RISK_TOO_HIGH"]}},"estimated_tokens":{"type":"integer","minimum":0},"estimated_cost_usd":{"type":"number","minimum":0},"suggested_alternative_model":{"type":"string"},"required_task_changes":{"type":"array","items":{"type":"string"}}},"required":["accept","confidence","reason_codes","estimated_tokens","estimated_cost_usd","suggested_alternative_model","required_task_changes"],"additionalProperties":false}`

var _ RuntimeAdapter = (*CLIExecutor)(nil)

// NewClaudeCLIExecutor creates an executor that shells out to the claude CLI.
// Requires claude (Claude Code) to be installed and already logged in.
func NewClaudeCLIExecutor(model string) *CLIExecutor {
	return &CLIExecutor{
		binary: "claude",
		model:  model,
	}
}

// RuntimeID returns the executor's runtime identifier.
func (*CLIExecutor) RuntimeID() string { return "claude-cli" }

func (e *CLIExecutor) admissionArgs(prompt string) []string {
	return []string{
		"-p", "--model", e.model,
		"--safe-mode",
		"--tools", "",
		"--no-session-persistence",
		"--output-format", "json",
		"--json-schema", admissionJSONSchema,
		prompt,
	}
}

func (e *CLIExecutor) executionArgs(prompt string) []string {
	return []string{"-p", "--model", e.model, "--output-format", "text", prompt}
}

// Run invokes the CLI and returns stdout as the model's response.
func (e *CLIExecutor) Run(ctx context.Context, prompt string) Result {
	cmd := exec.CommandContext(ctx, e.binary, e.admissionArgs(prompt)...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{Error: fmt.Errorf("claude cli admission: timed out")}
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return Result{Error: fmt.Errorf("claude cli: %s", detail)}
	}
	out, err := parseClaudeStructuredResult(stdout.Bytes())
	if err != nil {
		return Result{Error: fmt.Errorf("claude cli: %w", err)}
	}
	return Result{Output: out}
}

type claudeStructuredResult struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

func parseClaudeStructuredResult(data []byte) (string, error) {
	var result claudeStructuredResult
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("decode structured response: %w", err)
	}
	if result.IsError {
		return "", fmt.Errorf("%s", strings.TrimSpace(result.Result))
	}
	if structured := bytes.TrimSpace(result.StructuredOutput); len(structured) > 0 && !bytes.Equal(structured, []byte("null")) {
		return string(structured), nil
	}
	if fallback := strings.TrimSpace(result.Result); fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("empty response")
}

// Execute invokes the CLI for a full task. Claude Code owns its own context
// and output controls, so the shared token option is intentionally not added
// to the command line; the result still satisfies the same execution
// contract, with provider usage and truncation remaining unknown.
func (e *CLIExecutor) Execute(ctx context.Context, prompt string, _ ExecutionOptions) Result {
	cmd := exec.CommandContext(ctx, e.binary, e.executionArgs(prompt)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{Error: fmt.Errorf("claude cli execution: timed out")}
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return Result{Error: fmt.Errorf("claude cli: %s", detail)}
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return Result{Error: fmt.Errorf("claude cli: empty response")}
	}
	return Result{Output: out}
}

// EffectiveTools returns the tool set available in claude -p during execution.
func (e *CLIExecutor) EffectiveTools() []string {
	return []string{"bash", "read", "write", "edit"}
}

// Stream invokes the CLI and pipes tokens directly to w as they arrive.
// Returns an error if the process fails; output is already written to w on success.
func (e *CLIExecutor) Stream(ctx context.Context, prompt string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, e.binary, e.executionArgs(prompt)...)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
