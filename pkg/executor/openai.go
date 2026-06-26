package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	openAIEndpoint    = "https://api.openai.com/v1/chat/completions"
	openRouterEndpoint = "https://openrouter.ai/api/v1/chat/completions"
)

// OpenAIExecutor calls the OpenAI Chat Completions API.
// Reused for OpenRouter by pointing at a different endpoint.
type OpenAIExecutor struct {
	apiKey   string
	model    string
	endpoint string
	client   *http.Client
}

// NewOpenAIExecutor creates an executor for the given OpenAI model.
func NewOpenAIExecutor(apiKey, model string) *OpenAIExecutor {
	return &OpenAIExecutor{apiKey: apiKey, model: model, endpoint: openAIEndpoint, client: &http.Client{}}
}

// NewOpenRouterExecutor creates an executor for OpenRouter (OpenAI-compatible API).
func NewOpenRouterExecutor(apiKey, model string) *OpenAIExecutor {
	return &OpenAIExecutor{apiKey: apiKey, model: model, endpoint: openRouterEndpoint, client: &http.Client{}}
}

type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run sends prompt to the OpenAI-compatible API and returns the text response.
func (e *OpenAIExecutor) Run(ctx context.Context, prompt string) Result {
	body, err := json.Marshal(openAIRequest{
		Model:     e.model,
		Messages:  []openAIMessage{{Role: "user", Content: prompt}},
		MaxTokens: admissionMaxTokens,
	})
	if err != nil {
		return Result{Error: fmt.Errorf("openai marshal: %w", err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{Error: fmt.Errorf("openai request: %w", err)}
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if e.endpoint == openRouterEndpoint {
		req.Header.Set("HTTP-Referer", "https://github.com/oleg-koval/veto")
		req.Header.Set("X-Title", "veto")
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return Result{Error: fmt.Errorf("openai http: %w", err)}
	}
	defer resp.Body.Close()

	var ar openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return Result{Error: fmt.Errorf("openai decode: %w", err)}
	}
	if ar.Error != nil {
		return Result{Error: fmt.Errorf("openai api: %s", ar.Error.Message)}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{Error: fmt.Errorf("openai status %d", resp.StatusCode)}
	}
	if len(ar.Choices) == 0 {
		return Result{Error: fmt.Errorf("openai: empty response")}
	}
	return Result{Output: ar.Choices[0].Message.Content}
}
