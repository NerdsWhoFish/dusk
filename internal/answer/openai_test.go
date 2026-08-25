package answer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/answer"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

func TestOpenAIChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Errorf("Authorization = %q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["model"] != "model-a" {
			t.Errorf("model = %v", request["model"])
		}
		if request["max_completion_tokens"] != float64(4_096) {
			t.Errorf("max_completion_tokens = %v", request["max_completion_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Grounded answer [S1]"}}]}`))
	}))
	t.Cleanup(server.Close)

	client := &answer.OpenAI{BaseURL: server.URL + "/v1", APIKey: secret.New("provider-secret")}
	got, err := client.Complete(t.Context(), "model-a", []answer.Message{{Role: "user", Content: "question"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "Grounded answer [S1]" {
		t.Errorf("answer = %q", got)
	}
}

func TestOpenAIReadsTextContentParts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":[{"type":"text","text":"one"},{"type":"output_text","text":" two"}]}}]}`))
	}))
	t.Cleanup(server.Close)

	client := &answer.OpenAI{BaseURL: server.URL, APIKey: secret.New("provider-secret")}
	got, err := client.Complete(t.Context(), "model-a", nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "one two" {
		t.Errorf("answer = %q", got)
	}
}

func TestOpenAIProviderErrorIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	t.Cleanup(server.Close)

	client := &answer.OpenAI{BaseURL: server.URL, APIKey: secret.New("provider-secret")}
	_, err := client.Complete(t.Context(), "model-a", nil)
	if err == nil || !strings.Contains(err.Error(), "status 429: slow down") {
		t.Fatalf("Complete error = %v", err)
	}
}
