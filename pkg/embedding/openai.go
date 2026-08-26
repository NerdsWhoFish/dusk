// Package embedding calls an OpenAI-compatible embeddings endpoint.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

const maxResponse = 32 << 20

// OpenAI embeds batches of text through the common POST /embeddings contract.
type OpenAI struct {
	BaseURL string
	APIKey  secret.String
	Model   string
	HTTP    *http.Client
}

type request struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type response struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed returns one finite, non-empty vector per input in input order.
func (c *OpenAI) Embed(ctx context.Context, input []string) ([][]float32, error) {
	if len(input) == 0 {
		return nil, nil
	}
	req, err := c.newRequest(ctx, input)
	if err != nil {
		return nil, err
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding: request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	return decode(res, len(input))
}

func (c *OpenAI) newRequest(ctx context.Context, input []string) (*http.Request, error) {
	body, err := json.Marshal(request{Model: c.Model, Input: input})
	if err != nil {
		return nil, fmt.Errorf("embedding: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.BaseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedding: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if !c.APIKey.IsZero() {
		req.Header.Set("Authorization", "Bearer "+c.APIKey.Reveal())
	}
	return req, nil
}

func decode(res *http.Response, count int) ([][]float32, error) {
	encoded, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("embedding: read response: %w", err)
	}
	if len(encoded) > maxResponse {
		return nil, fmt.Errorf("embedding: response exceeds %d bytes", maxResponse)
	}
	var decoded response
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("embedding: decode response with status %d: %w", res.StatusCode, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
			return nil, fmt.Errorf("embedding: provider returned status %d: %.512s", res.StatusCode, decoded.Error.Message)
		}
		return nil, fmt.Errorf("embedding: provider returned status %d", res.StatusCode)
	}
	if decoded.Error != nil {
		return nil, errors.New("embedding: provider returned an error: " + decoded.Error.Message)
	}
	if len(decoded.Data) != count {
		return nil, fmt.Errorf("embedding: provider returned %d vectors for %d inputs", len(decoded.Data), count)
	}
	return ordered(decoded, count)
}

func ordered(decoded response, count int) ([][]float32, error) {
	vectors := make([][]float32, count)
	dimensions := 0
	for _, item := range decoded.Data {
		if err := validateItem(item.Index, item.Embedding, vectors, dimensions); err != nil {
			return nil, err
		}
		if dimensions == 0 {
			dimensions = len(item.Embedding)
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}

func validateItem(index int, vector []float32, vectors [][]float32, dimensions int) error {
	if index < 0 || index >= len(vectors) || vectors[index] != nil {
		return fmt.Errorf("embedding: invalid response index %d", index)
	}
	if len(vector) == 0 {
		return errors.New("embedding: provider returned an empty vector")
	}
	if dimensions != 0 && len(vector) != dimensions {
		return errors.New("embedding: provider returned vectors with different dimensions")
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return errors.New("embedding: provider returned a non-finite vector")
		}
	}
	return nil
}

func (c *OpenAI) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}
