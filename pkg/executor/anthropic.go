package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	anthropicEndpoint    = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion  = "2023-06-01"
	admissionMaxTokens   = 512
)

// AnthropicExecutor calls the Anthropic Messages API.
type AnthropicExecutor struct {
	apiKey string
	model  string
	client *http.Client
}

// NewAnthropicExecutor creates an executor for the given Anthropic model.
func NewAnthropicExecutor(apiKey, model string) *AnthropicExecutor {
	return &AnthropicExecutor{apiKey: apiKey, model: model, client: &http.Client{}}
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

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run sends prompt to the Anthropic Messages API and returns the text response.
func (e *AnthropicExecutor) Run(ctx context.Context, prompt string) Result {
	body, err := json.Marshal(anthropicRequest{
		Model:     e.model,
		MaxTokens: admissionMaxTokens,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return Result{Error: fmt.Errorf("anthropic marshal: %w", err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewReader(body))
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
	defer resp.Body.Close()

	var ar anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return Result{Error: fmt.Errorf("anthropic decode: %w", err)}
	}
	if ar.Error != nil {
		return Result{Error: fmt.Errorf("anthropic api: %s", ar.Error.Message)}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{Error: fmt.Errorf("anthropic status %d", resp.StatusCode)}
	}

	var parts []string
	for _, c := range ar.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return Result{Output: strings.Join(parts, "")}
}
