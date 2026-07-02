package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CLIExecutor runs a subscription CLI (e.g. claude -p) to answer prompts.
// Cost is $0 marginal — the user pays a flat subscription, not per token.
type CLIExecutor struct {
	binary   string   // "claude"
	args     []string // flags that precede the prompt, e.g. ["-p", "--model", "...", "--output-format", "text"]
}

// NewClaudeCLIExecutor creates an executor that shells out to the claude CLI.
// Requires claude (Claude Code) to be installed and already logged in.
func NewClaudeCLIExecutor(model string) *CLIExecutor {
	return &CLIExecutor{
		binary: "claude",
		args:   []string{"-p", "--model", model, "--output-format", "text"},
	}
}

// Run invokes the CLI and returns stdout as the model's response.
func (e *CLIExecutor) Run(ctx context.Context, prompt string) Result {
	args := append(e.args, prompt) //nolint:gocritic // intentional: append to copy
	cmd := exec.CommandContext(ctx, e.binary, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{Error: fmt.Errorf("claude cli: timed out (use --timeout to increase)")}
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

// EffectiveTools returns the tools claude -p can use during execution.
// CLIExecutor is the only executor that actually invokes tools — HTTP executors
// are text-only and do not implement ToolProvider.
func (e *CLIExecutor) EffectiveTools() []string {
	return []string{"bash", "read", "write", "edit"}
}

// Stream invokes the CLI and pipes tokens directly to w as they arrive.
// Returns an error if the process fails; output is already written to w on success.
func (e *CLIExecutor) Stream(ctx context.Context, prompt string, w io.Writer) error {
	args := append(e.args, prompt) //nolint:gocritic // intentional: append to copy
	cmd := exec.CommandContext(ctx, e.binary, args...)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
