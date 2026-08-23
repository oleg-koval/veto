package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	openAIEndpoint     = "https://api.openai.com/v1/chat/completions"
	openRouterEndpoint = "https://openrouter.ai/api/v1/chat/completions"
	xAIEndpoint        = "https://api.x.ai/v1/chat/completions"
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

// NewOpenAICompatibleExecutor targets any OpenAI-compatible chat-completions
// endpoint (Ollama, LM Studio, vLLM, llama.cpp server). apiKey may be empty.
func NewOpenAICompatibleExecutor(apiKey, model, endpoint string) *OpenAIExecutor {
	return &OpenAIExecutor{apiKey: apiKey, model: model, endpoint: endpoint, client: &http.Client{}}
}

// NewXAIExecutor creates an executor for xAI Grok models via the OpenAI-compatible
// endpoint at api.x.ai. Use XAI_API_KEY.
func NewXAIExecutor(apiKey, model string) *OpenAIExecutor {
	return &OpenAIExecutor{apiKey: apiKey, model: model, endpoint: xAIEndpoint, client: &http.Client{}}
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

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run sends prompt to the OpenAI-compatible API and returns the text response.
// It is the short admission-probe path and always uses the 512-token budget.
func (e *OpenAIExecutor) Run(ctx context.Context, prompt string) Result {
	return e.run(ctx, prompt, admissionMaxTokens)
}

// Execute sends a full task prompt with an explicit bounded output budget.
func (e *OpenAIExecutor) Execute(ctx context.Context, prompt string, options ExecutionOptions) Result {
	return e.run(ctx, prompt, options.maxOutputTokens())
}

// run sends prompt to the OpenAI-compatible API.
// Retries up to maxRetries times on transient server errors (429, 502, 503, 504).
func (e *OpenAIExecutor) run(ctx context.Context, prompt string, maxTokens int) Result {
	body, err := json.Marshal(openAIRequest{
		Model:     e.model,
		Messages:  []openAIMessage{{Role: "user", Content: prompt}},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return Result{Error: fmt.Errorf("openai marshal: %w", err)}
	}

	bodyReader := bytes.NewReader(body)
	for attempt := 1; attempt <= maxRetries; attempt++ {
		bodyReader.Seek(0, io.SeekStart) //nolint:errcheck // bytes.Reader.Seek never fails for SeekStart
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bodyReader)
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
			if attempt == 1 && tryStartOllama(e.endpoint) {
				bodyReader.Seek(0, io.SeekStart) //nolint:errcheck
				req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bodyReader)
				req2.Header = req.Header.Clone()
				if resp2, err2 := e.client.Do(req2); err2 == nil {
					resp = resp2
					err = nil
				}
			}
			if err != nil {
				return Result{Error: fmt.Errorf("openai http: %w", err)}
			}
		}

		if retryableStatus(resp.StatusCode) && attempt < maxRetries {
			delay := retryAfter(resp, retryDelay(attempt))
			resp.Body.Close()
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return Result{Error: fmt.Errorf("openai: %w", ctx.Err())}
			case <-t.C:
				continue
			}
		}

		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return Result{Error: fmt.Errorf("openai read: %w", readErr)}
		}
		var ar openAIResponse
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			if json.Unmarshal(data, &ar) == nil && ar.Error != nil {
				return Result{Error: fmt.Errorf("openai api: %s", ar.Error.Message)}
			}
			return Result{Error: fmt.Errorf("openai status %d: %s", resp.StatusCode, responseSnippet(data))}
		}
		if decErr := json.Unmarshal(data, &ar); decErr != nil {
			return Result{Error: fmt.Errorf("openai decode: %w", decErr)}
		}
		if ar.Error != nil {
			return Result{Error: fmt.Errorf("openai api: %s", ar.Error.Message)}
		}
		if len(ar.Choices) == 0 {
			return Result{Error: fmt.Errorf("openai: empty response")}
		}
		choice := ar.Choices[0]
		output := strings.TrimSpace(choice.Message.Content)
		if output == "" {
			return Result{Error: fmt.Errorf("openai: empty response")}
		}
		result := Result{
			Output:       output,
			FinishReason: choice.FinishReason,
			Truncated:    isTruncationReason(choice.FinishReason),
		}
		if ar.Usage != nil {
			result.Usage = Usage{
				InputTokens:  ar.Usage.PromptTokens,
				OutputTokens: ar.Usage.CompletionTokens,
				TotalTokens:  ar.Usage.TotalTokens,
				Known:        true,
			}
		}
		return result
	}
	return Result{Error: fmt.Errorf("openai: max retries exceeded")}
}

func responseSnippet(data []byte) string {
	const maxSnippet = 512
	snippet := strings.TrimSpace(string(data))
	if len(snippet) > maxSnippet {
		return snippet[:maxSnippet] + "..."
	}
	return snippet
}

func isTruncationReason(reason string) bool {
	return reason == "length" || reason == "max_tokens"
}

// tryStartOllama starts `ollama serve` in the background when the endpoint is
// the local Ollama server and the binary is available. Blocks up to 5s waiting
// for the server to become ready. Returns true if the server is now reachable.
func tryStartOllama(endpoint string) bool {
	if !strings.Contains(endpoint, "localhost:11434") && !strings.Contains(endpoint, "127.0.0.1:11434") {
		return false
	}
	if _, err := exec.LookPath("ollama"); err != nil {
		return false
	}
	srv := exec.Command("ollama", "serve")
	srv.Stdout = nil
	srv.Stderr = nil
	if err := srv.Start(); err != nil {
		return false
	}
	// Poll until ready (max ~5s).
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := client.Get("http://localhost:11434")
		if err == nil {
			resp.Body.Close()
			return true
		}
	}
	return false
}
