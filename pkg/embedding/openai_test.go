package embedding_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/embedding"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

func TestOpenAIEmbedsABatchInInputOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request = %s, authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "all-minilm" || !slices.Equal(body.Input, []string{"one", "two"}) {
			t.Fatalf("body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()

	client := &embedding.OpenAI{
		BaseURL: server.URL + "/v1", APIKey: secret.New("secret"), Model: "all-minilm",
	}
	vectors, err := client.Embed(t.Context(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !slices.Equal(vectors[0], []float32{1, 0}) || !slices.Equal(vectors[1], []float32{0, 1}) {
		t.Fatalf("vectors = %v", vectors)
	}
}

func TestOpenAIEmbeddingsAcceptsAKeylessLocalEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1]}]}`))
	}))
	defer server.Close()

	_, err := (&embedding.OpenAI{BaseURL: server.URL, Model: "local"}).Embed(t.Context(), []string{"one"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
}
