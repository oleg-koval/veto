package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CodexCLIExecutor runs Codex through the user's existing CLI login.
type CodexCLIExecutor struct {
	binary          string
	admissionSchema string
	costKnown       bool
}

var _ RuntimeAdapter = (*CodexCLIExecutor)(nil)
var _ EventTaskExecutor = (*CodexCLIExecutor)(nil)

const (
	maxCodexEventLine  = 8 << 20
	maxCodexEventBytes = 64 << 20
)

func NewCodexCLIExecutor() *CodexCLIExecutor {
	return &CodexCLIExecutor{binary: "codex", admissionSchema: admissionJSONSchema, costKnown: true}
}

// NewCodexCLIExecutorWithUnknownCost preserves API-key and unrecognized Codex
// authentication as billable-unknown rather than subscription-free.
func NewCodexCLIExecutorWithUnknownCost() *CodexCLIExecutor {
	schema := strings.Replace(admissionJSONSchema, `"minimum":0,"maximum":0`, `"minimum":0`, 1)
	return &CodexCLIExecutor{binary: "codex", admissionSchema: schema}
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
	if err := os.WriteFile(schemaPath, []byte(e.admissionSchema), 0600); err != nil {
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

func (e *CodexCLIExecutor) Execute(ctx context.Context, prompt string, options ExecutionOptions) Result {
	return e.ExecuteWithEvents(ctx, prompt, options, io.Discard, nil)
}

func (e *CodexCLIExecutor) Stream(ctx context.Context, prompt string, w io.Writer) error {
	return e.ExecuteWithEvents(ctx, prompt, ExecutionOptions{}, w, nil).Error
}

func (*CodexCLIExecutor) executionArgs(prompt string) []string {
	return []string{"exec", "--ephemeral", "--json", "--color", "never", prompt}
}

// ExecuteWithEvents consumes Codex's JSONL stream so long-running agent work
// remains observable without exposing command arguments or command output.
func (e *CodexCLIExecutor) ExecuteWithEvents(
	ctx context.Context,
	prompt string,
	options ExecutionOptions,
	w io.Writer,
	emit func(RuntimeEvent),
) Result {
	if options.MaxOutputTokens > 0 && options.MaxOutputTokens != DefaultExecutionMaxTokens {
		return Result{
			Error:     fmt.Errorf("codex cli does not support custom --max-output-tokens; use the default %d", DefaultExecutionMaxTokens),
			CostKnown: e.costKnown,
		}
	}
	if w == nil {
		w = io.Discard
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := commandContext(runCtx, e.binary, e.executionArgs(prompt)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Error: fmt.Errorf("codex cli execution: open event stream: %w", err)}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return codexCLIError(ctx, "execution", err, stderr.String())
	}

	state := codexExecutionState{writer: w, emit: emit, costKnown: e.costKnown}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxCodexEventLine)
	for scanner.Scan() {
		state.eventBytes += len(scanner.Bytes()) + 1
		if state.eventBytes > maxCodexEventBytes {
			state.failure = errors.New("codex cli execution event output exceeds the size limit")
			cancel()
			break
		}
		if err := state.process(scanner.Bytes()); err != nil {
			state.failure = err
			cancel()
			break
		}
	}
	if err := scanner.Err(); err != nil && state.failure == nil {
		state.failure = fmt.Errorf("read codex cli execution events: %w", err)
		cancel()
	}
	runErr := cmd.Wait()
	result := state.result()
	if ctx.Err() != nil {
		return codexCLIError(ctx, "execution", ctx.Err(), stderr.String())
	}
	if state.failure != nil {
		result.Error = state.failure
		return result
	}
	if runErr != nil {
		return codexCLIError(ctx, "execution", runErr, stderr.String())
	}
	if result.Output == "" {
		result.Error = fmt.Errorf("codex cli execution: empty response")
	}
	return result
}

type codexExecutionState struct {
	writer     io.Writer
	emit       func(RuntimeEvent)
	output     string
	usage      Usage
	failure    error
	eventBytes int
	costKnown  bool
}

func (s *codexExecutionState) process(line []byte) error {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"item"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return fmt.Errorf("decode codex cli execution event: %w", err)
	}

	switch event.Type {
	case "item.started", "item.completed":
		if event.Type == "item.completed" && event.Item.Type == "agent_message" {
			message := strings.TrimSpace(event.Item.Text)
			if message == "" {
				return nil
			}
			if _, err := io.WriteString(s.writer, message+"\n"); err != nil {
				return fmt.Errorf("write codex cli output: %w", err)
			}
			s.output = message
			return nil
		}
		s.emitToolEvent(event.Type, event.Item.Type, event.Item.Status)
	case "turn.completed":
		input := nonNegativeCodexTokens(event.Usage.InputTokens)
		output := nonNegativeCodexTokens(event.Usage.OutputTokens)
		s.usage = Usage{InputTokens: input, OutputTokens: output, TotalTokens: input + output, Known: true}
	case "turn.failed", "error":
		message := strings.TrimSpace(event.Error.Message)
		if message == "" {
			message = strings.TrimSpace(event.Message)
		}
		if message == "" {
			message = event.Type
		}
		s.failure = fmt.Errorf("codex cli execution: %s", message)
	}
	return nil
}

func (s *codexExecutionState) emitToolEvent(eventType, itemType, status string) {
	if s.emit == nil {
		return
	}
	name := ""
	switch itemType {
	case "command_execution":
		name = "shell"
	case "file_change":
		name = "edit"
	case "mcp_tool_call":
		name = "mcp"
	case "web_search":
		name = "web"
	default:
		return
	}
	event := RuntimeEvent{Name: name}
	if eventType == "item.started" {
		event.Kind, event.Status = RuntimeToolStarted, "running"
	} else if status == "failed" {
		event.Kind, event.Status = RuntimeToolError, "error"
	} else {
		event.Kind, event.Status = RuntimeToolCompleted, "completed"
	}
	s.emit(event)
}

func (s *codexExecutionState) result() Result {
	return Result{
		Output: s.output, Usage: s.usage, Error: s.failure,
		CostUSD: 0, CostKnown: s.costKnown, FinishReason: "completed",
	}
}

func nonNegativeCodexTokens(value int) int {
	if value < 0 {
		return 0
	}
	return value
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
