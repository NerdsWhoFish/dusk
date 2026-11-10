package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/telemetry"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRefusedPreviewCommentIsNotAnErrorSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	var posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues/"):
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues/"):
			posted = true
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"Resource not accessible by integration","status":"403"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	repository := "example/homelab"
	ref := PreviewRef(repository, 7)
	if err := idx.Put(t.Context(), repository, ref, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	client := &githubapp.Client{BaseURL: server.URL, HTTP: telemetry.HTTPClient(0)}
	install := &githubapp.Install{
		Client: client,
		Tokens: &githubapp.Tokens{Client: client, App: testApp(t)},
		ID:     7,
	}
	var logs bytes.Buffer
	c := &Controller{opts: Options{
		Index:  idx,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}}
	preview := Preview{Owner: "example", Name: "homelab", Number: 7}
	if err := c.commentOnPreview(t.Context(), install, preview); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("comment was not attempted")
	}
	if !strings.Contains(logs.String(), "could not comment on the pull request") {
		t.Fatalf("log = %s, want the refused comment", logs.String())
	}

	span := spanWhoseURLContains(recorder.Ended(), "/issues/7/comments")
	if span == nil {
		t.Fatalf("missing comment span: %v", recorder.Ended())
	}
	if span.Name() != "HTTP POST" {
		t.Fatalf("span = %s, want the comment post", span.Name())
	}
	if span.Status().Code != codes.Ok {
		t.Fatalf("span status = %s, want ok", span.Status().Code)
	}
	var status int64
	var sawStatus bool
	for _, attr := range span.Attributes() {
		if attr.Key == "http.response.status_code" {
			status = attr.Value.AsInt64()
			sawStatus = true
		}
	}
	if !sawStatus || status != http.StatusForbidden {
		t.Fatalf("http.response.status_code = %d present=%v, want 403", status, sawStatus)
	}
}

func spanWhoseURLContains(spans []sdktrace.ReadOnlySpan, part string) sdktrace.ReadOnlySpan {
	for _, span := range spans {
		for _, attr := range span.Attributes() {
			if attr.Key == "url.full" && strings.Contains(attr.Value.AsString(), part) && strings.Contains(span.Name(), "POST") {
				return span
			}
		}
	}
	return nil
}

func testApp(t *testing.T) githubapp.App {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return githubapp.App{ID: 42, PrivateKey: secret.New(string(encoded))}
}
