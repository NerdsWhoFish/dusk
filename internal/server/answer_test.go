package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/answer"
)

type answerCompleter struct{}

func (answerCompleter) Complete(_ context.Context, _ string, messages []answer.Message, _ []answer.ToolDefinition) (answer.Completion, error) {
	for _, message := range messages {
		if message.Role == "tool" {
			return answer.Completion{Content: "The catalog has no matching evidence."}, nil
		}
	}
	return answer.Completion{ToolCalls: []answer.ToolCall{{
		ID: "search-1", Type: "function",
		Function: answer.FunctionCall{Name: "search_estate", Arguments: `{"query":"missing"}`},
	}}}, nil
}

func TestAIConfigurationIsDisabledWithoutAProvider(t *testing.T) {
	handler := build(t, setup{
		store: registered(), catalog: emptyCatalog(t),
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})
	recorder := get(t, handler, "/api/ai")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/ai = %d: %s", recorder.Code, recorder.Body.String())
	}
	var config answer.Configuration
	if err := json.Unmarshal(recorder.Body.Bytes(), &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Enabled {
		t.Fatal("AI config is enabled without a provider")
	}
}

func TestAIQuestionUsesTheSeparatePostRoute(t *testing.T) {
	catalog := emptyCatalog(t)
	handler := build(t, setup{
		store: registered(), catalog: catalog,
		answers: &answer.Service{
			Catalog: catalog, Completer: answerCompleter{}, Models: []string{"model-a"},
			DefaultModel: "model-a", Provider: "provider.example",
		},
		env: map[string]string{"DUSK_TRUSTED_NETWORK": "true"},
	})
	body := bytes.NewBufferString(`{"question":"What is missing?","model":"model-a"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/ai/ask", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST /api/ai/ask = %d: %s", recorder.Code, recorder.Body.String())
	}
	var result answer.Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Model != "model-a" || result.Answer == "" {
		t.Fatalf("answer = %+v", result)
	}
}
