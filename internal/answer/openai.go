package answer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

const (
	maxCompletionTokens = 4_096
	maxProviderResponse = 2 << 20
)

// OpenAI calls an OpenAI-compatible Chat Completions endpoint.
type OpenAI struct {
	BaseURL string
	APIKey  secret.String
	HTTP    *http.Client
}

type completionRequest struct {
	Model               string           `json:"model"`
	Messages            []Message        `json:"messages"`
	MaxCompletionTokens int              `json:"max_completion_tokens"`
	Tools               []ToolDefinition `json:"tools,omitempty"`
}

type completionResponse struct {
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []ToolCall      `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete exchanges one turn with the configured Chat Completions endpoint.
func (c *OpenAI) Complete(ctx context.Context, model string, messages []Message, tools []ToolDefinition) (Completion, error) {
	body, err := json.Marshal(completionRequest{
		Model: model, Messages: messages, MaxCompletionTokens: maxCompletionTokens, Tools: tools,
	})
	if err != nil {
		return Completion{}, fmt.Errorf("openai: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Completion{}, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey.Reveal())

	response, err := c.httpClient().Do(req)
	if err != nil {
		return Completion{}, fmt.Errorf("openai: chat completion: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	return decodeCompletion(response)
}

func decodeCompletion(response *http.Response) (Completion, error) {
	limited := io.LimitReader(response.Body, maxProviderResponse+1)
	encoded, err := io.ReadAll(limited)
	if err != nil {
		return Completion{}, fmt.Errorf("openai: read response: %w", err)
	}
	if len(encoded) > maxProviderResponse {
		return Completion{}, fmt.Errorf("openai: response exceeds %d bytes", maxProviderResponse)
	}

	var decoded completionResponse
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return Completion{}, fmt.Errorf("openai: decode response with status %d: %w", response.StatusCode, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
			return Completion{}, fmt.Errorf("openai: provider returned status %d: %s", response.StatusCode, bounded(decoded.Error.Message, 512))
		}
		return Completion{}, fmt.Errorf("openai: provider returned status %d", response.StatusCode)
	}
	if decoded.Error != nil {
		return Completion{}, errors.New("openai: provider returned an error in a successful response: " + bounded(decoded.Error.Message, 512))
	}
	if len(decoded.Choices) == 0 {
		return Completion{}, errors.New("openai: response contains no choices")
	}
	content, err := textContent(decoded.Choices[0].Message.Content)
	if err != nil {
		return Completion{}, err
	}
	return Completion{Content: content, ToolCalls: decoded.Choices[0].Message.ToolCalls}, nil
}

func (c *OpenAI) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func textContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("openai: message content is not text: %w", err)
	}
	var out strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "output_text" || part.Type == "" {
			out.WriteString(part.Text)
		}
	}
	return out.String(), nil
}
