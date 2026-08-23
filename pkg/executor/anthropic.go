package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicEndpoint   = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion = "2023-06-01"
	admissionMaxTokens  = 512
)

// AnthropicExecutor calls the Anthropic Messages API.
type AnthropicExecutor struct {
	apiKey   string
	model    string
	endpoint string // defaults to anthropicEndpoint; overridable in tests
	client   *http.Client
}

// NewAnthropicExecutor creates an executor for the given Anthropic model.
func NewAnthropicExecutor(apiKey, model string) *AnthropicExecutor {
	return &AnthropicExecutor{apiKey: apiKey, model: model, endpoint: anthropicEndpoint, client: &http.Client{}}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      *anthropicUsage    `json:"usage"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run sends prompt to the Anthropic Messages API and returns the text response.
// It is the short admission-probe path and always uses the 512-token budget.
func (e *AnthropicExecutor) Run(ctx context.Context, prompt string) Result {
	return e.run(ctx, prompt, admissionMaxTokens)
}

// Execute sends a full task prompt with an explicit bounded output budget.
func (e *AnthropicExecutor) Execute(ctx context.Context, prompt string, options ExecutionOptions) Result {
	return e.run(ctx, prompt, options.maxOutputTokens())
}

// run sends prompt to the Anthropic Messages API.
// Retries up to maxRetries times on transient server errors (429, 502, 503, 504).
func (e *AnthropicExecutor) run(ctx context.Context, prompt string, maxTokens int) Result {
	body, err := json.Marshal(anthropicRequest{
		Model:     e.model,
		MaxTokens: maxTokens,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return Result{Error: fmt.Errorf("anthropic marshal: %w", err)}
	}

	bodyReader := bytes.NewReader(body)
	for attempt := 1; attempt <= maxRetries; attempt++ {
		bodyReader.Seek(0, io.SeekStart) //nolint:errcheck // bytes.Reader.Seek never fails for SeekStart
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bodyReader)
		if err != nil {
			return Result{Error: fmt.Errorf("anthropic request: %w", err)}
		}
		req.Header.Set("x-api-key", e.apiKey)
		req.Header.Set("anthropic-version", anthropicAPIVersion)
		req.Header.Set("content-type", "application/json")

		resp, err := e.client.Do(req)
		if err != nil {
			return Result{Error: fmt.Errorf("anthropic http: %w", err)}
		}

		if retryableStatus(resp.StatusCode) && attempt < maxRetries {
			delay := retryAfter(resp, retryDelay(attempt))
			resp.Body.Close()
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return Result{Error: fmt.Errorf("anthropic: %w", ctx.Err())}
			case <-t.C:
				continue
			}
		}

		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return Result{Error: fmt.Errorf("anthropic read: %w", readErr)}
		}
		var ar anthropicResponse
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			if json.Unmarshal(data, &ar) == nil && ar.Error != nil {
				return Result{Error: fmt.Errorf("anthropic api: %s", ar.Error.Message)}
			}
			return Result{Error: fmt.Errorf("anthropic status %d: %s", resp.StatusCode, responseSnippet(data))}
		}
		if decErr := json.Unmarshal(data, &ar); decErr != nil {
			return Result{Error: fmt.Errorf("anthropic decode: %w", decErr)}
		}
		if ar.Error != nil {
			return Result{Error: fmt.Errorf("anthropic api: %s", ar.Error.Message)}
		}

		var parts []string
		for _, c := range ar.Content {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			}
		}
		output := strings.TrimSpace(strings.Join(parts, ""))
		if output == "" {
			return Result{Error: fmt.Errorf("anthropic: empty response")}
		}
		result := Result{
			Output:       output,
			FinishReason: ar.StopReason,
			Truncated:    ar.StopReason == "max_tokens",
		}
		if ar.Usage != nil {
			result.Usage = Usage{
				InputTokens:  ar.Usage.InputTokens,
				OutputTokens: ar.Usage.OutputTokens,
				TotalTokens:  ar.Usage.InputTokens + ar.Usage.OutputTokens,
				Known:        true,
			}
		}
		return result
	}
	return Result{Error: fmt.Errorf("anthropic: max retries exceeded")}
}
