package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CodexCLIExecutor runs Codex through the user's existing ChatGPT login.
type CodexCLIExecutor struct {
	binary string
}

var _ RuntimeAdapter = (*CodexCLIExecutor)(nil)

func NewCodexCLIExecutor() *CodexCLIExecutor {
	return &CodexCLIExecutor{binary: "codex"}
}

func (*CodexCLIExecutor) RuntimeID() string { return "codex-cli" }

func (*CodexCLIExecutor) EffectiveTools() []string {
	return []string{"bash", "read", "write", "edit"}
}

func (e *CodexCLIExecutor) Run(ctx context.Context, prompt string) Result {
	dir, err := os.MkdirTemp("", "veto-codex-admission-")
	if err != nil {
		return Result{Error: fmt.Errorf("codex cli admission: create workspace: %w", err)}
	}
	defer os.RemoveAll(dir)

	schemaPath := filepath.Join(dir, "schema.json")
	outputPath := filepath.Join(dir, "output.json")
	if err := os.WriteFile(schemaPath, []byte(admissionJSONSchema), 0600); err != nil {
		return Result{Error: fmt.Errorf("codex cli admission: write schema: %w", err)}
	}
	args := []string{
		"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules",
		"--sandbox", "read-only", "--skip-git-repo-check", "--color", "never",
		"--cd", dir, "--output-schema", schemaPath, "--output-last-message", outputPath,
		prompt,
	}
	cmd := commandContext(ctx, e.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return codexCLIError(ctx, "admission", err, stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return Result{Error: fmt.Errorf("codex cli admission: read structured response: %w", err)}
	}
	output := strings.TrimSpace(string(data))
	if output == "" {
		return Result{Error: fmt.Errorf("codex cli admission: empty response")}
	}
	return Result{Output: output}
}

func (e *CodexCLIExecutor) Execute(ctx context.Context, prompt string, _ ExecutionOptions) Result {
	cmd := commandContext(ctx, e.binary, e.executionArgs(prompt)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return codexCLIError(ctx, "execution", err, stderr.String())
	}
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return Result{Error: fmt.Errorf("codex cli execution: empty response")}
	}
	return Result{Output: output}
}

func (e *CodexCLIExecutor) Stream(ctx context.Context, prompt string, w io.Writer) error {
	cmd := commandContext(ctx, e.binary, e.executionArgs(prompt)...)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (*CodexCLIExecutor) executionArgs(prompt string) []string {
	return []string{"exec", "--ephemeral", "--color", "never", prompt}
}

func codexCLIError(ctx context.Context, phase string, commandErr error, stderr string) Result {
	if ctx.Err() != nil {
		return Result{Error: fmt.Errorf("codex cli %s: timed out", phase)}
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = commandErr.Error()
	}
	return Result{Error: fmt.Errorf("codex cli %s: %s", phase, detail)}
}
