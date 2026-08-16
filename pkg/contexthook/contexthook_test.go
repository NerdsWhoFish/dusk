package contexthook_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/pkg/contexthook"
)

// orientation stands in for what dusk_context renders: markdown with a heading,
// a pinned note and an inventory, so a test can assert it arrived whole.
const orientation = `# example/homelab in the catalog

## Pinned, about this repository

**gotcha** ` + "`.dusk/sops.md`" + `: nothing decrypts a sealed secret on the way in.

## What this operator has

- service: 3
`

// stub answers dusk_context the way Dusk does, over the transport an agent
// actually uses, so the tests exercise the wire rather than a fake client.
type stub struct {
	answer  string
	refuses bool
	refusal string
	token   string

	mu    sync.Mutex
	roots []string
}

type contextArgs struct {
	Root string `json:"root,omitempty"`
}

// serve mounts the stub at Path only, so a request to any other path is a 404
// and an endpoint that was not completed cannot pass by accident.
func (s *stub) serve(t *testing.T) string {
	t.Helper()

	server := sdk.NewServer(&sdk.Implementation{Name: "dusk", Version: "test"}, nil)
	sdk.AddTool(server, &sdk.Tool{
		Name:        contexthook.ToolName,
		Description: "What this operator's catalog knows.",
	}, s.answerContext)

	var handler http.Handler = sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server }, nil)
	if s.token != "" {
		handler = requireBearer(handler, s.token)
	}

	mux := http.NewServeMux()
	mux.Handle(contexthook.Path, handler)

	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

func (s *stub) answerContext(_ context.Context, _ *sdk.CallToolRequest, in contextArgs) (*sdk.CallToolResult, any, error) {
	s.mu.Lock()
	s.roots = append(s.roots, in.Root)
	s.mu.Unlock()

	if s.refuses {
		refusal := s.refusal
		if refusal == "" {
			refusal = "the catalog could not be read"
		}
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{Text: refusal}},
		}, nil, nil
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s.answer}}}, nil, nil
}

// asked is every root the tool was called with.
func (s *stub) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.roots...)
}

func requireBearer(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "the catalog requires a bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unreachable is the address of a server that has stopped, which is what a Dusk
// that is down looks like from a laptop.
func unreachable(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	return url
}

func TestFetchReturnsWhatTheToolAnswered(t *testing.T) {
	dusk := &stub{answer: orientation}
	endpoint := dusk.serve(t)

	body, err := contexthook.Fetch(t.Context(),
		contexthook.Options{Endpoint: endpoint, Timeout: 5 * time.Second}, "/src/example/homelab")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if body != orientation {
		t.Errorf("Fetch returned:\n%s\nwant:\n%s", body, orientation)
	}
	if asked := dusk.asked(); len(asked) != 1 || asked[0] != "/src/example/homelab" {
		t.Errorf("dusk_context was asked about %v, want [/src/example/homelab]", asked)
	}
}

func TestFetchPresentsTheBearerToken(t *testing.T) {
	dusk := &stub{answer: orientation, token: "sesame"}
	endpoint := dusk.serve(t)

	body, err := contexthook.Fetch(t.Context(),
		contexthook.Options{Endpoint: endpoint, Token: "sesame", Timeout: 5 * time.Second}, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if body != orientation {
		t.Errorf("Fetch returned %q, want the orientation", body)
	}
}

// An endpoint is the likeliest thing to be written down by hand, and asking the
// wrong URL is indistinguishable from a Dusk that is not there.
func TestFetchCompletesAnEndpointNamingOnlyAHost(t *testing.T) {
	dusk := &stub{answer: orientation}
	base := dusk.serve(t)

	tests := []struct {
		name     string
		endpoint string
		want     bool
	}{
		{name: "only a host", endpoint: base, want: true},
		{name: "a bare root path", endpoint: base + "/", want: true},
		{name: "the surface named in full", endpoint: base + contexthook.Path, want: true},
		{name: "some other path", endpoint: base + "/elsewhere", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := contexthook.Fetch(t.Context(),
				contexthook.Options{Endpoint: test.endpoint, Timeout: 5 * time.Second}, "")
			switch {
			case test.want && err != nil:
				t.Fatalf("Fetch(%q): %v", test.endpoint, err)
			case test.want && body != orientation:
				t.Errorf("Fetch(%q) returned %q, want the orientation", test.endpoint, body)
			case !test.want && err == nil:
				t.Errorf("Fetch(%q) answered, want an error", test.endpoint)
			}
		})
	}
}

func TestFetchWithoutAnEndpointIsNotConfigured(t *testing.T) {
	_, err := contexthook.Fetch(t.Context(), contexthook.Options{}, "/src/example/homelab")
	if !errors.Is(err, contexthook.ErrNotConfigured) {
		t.Fatalf("Fetch with no endpoint: %v, want ErrNotConfigured", err)
	}
}

func TestFetchReportsWhyItCouldNotAnswer(t *testing.T) {
	refusing := &stub{refuses: true}
	unauthenticated := &stub{answer: orientation, token: "sesame"}
	empty := &stub{answer: "   "}

	tests := []struct {
		name     string
		options  contexthook.Options
		contains string
	}{
		{
			name:     "not configured",
			options:  contexthook.Options{},
			contains: "no Dusk is configured",
		},
		{
			name:     "not a URL",
			options:  contexthook.Options{Endpoint: "dusk.example.com"},
			contains: "not an absolute URL",
		},
		{
			name:     "unreachable",
			options:  contexthook.Options{Endpoint: unreachable(t)},
			contains: "connecting to",
		},
		{
			name:     "no token where one is required",
			options:  contexthook.Options{Endpoint: unauthenticated.serve(t)},
			contains: "connecting to",
		},
		{
			name:     "the wrong token",
			options:  contexthook.Options{Endpoint: unauthenticated.serve(t), Token: "guess"},
			contains: "connecting to",
		},
		{
			name:     "the tool refused",
			options:  contexthook.Options{Endpoint: refusing.serve(t)},
			contains: "the catalog could not be read",
		},
		{
			name:     "an answer with no text",
			options:  contexthook.Options{Endpoint: empty.serve(t)},
			contains: "answered with no text",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := test.options
			options.Timeout = 5 * time.Second

			body, err := contexthook.Fetch(t.Context(), options, "")
			if err == nil {
				t.Fatalf("Fetch answered %q, want an error", body)
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Errorf("Fetch: %v, want it to mention %q", err, test.contains)
			}
		})
	}
}
