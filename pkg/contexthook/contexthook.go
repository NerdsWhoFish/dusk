// Package contexthook is the client-side half of ADR-0014's context injection:
// it asks a Dusk what an agent should know before it starts, and answers in the
// shape a session-start hook injects into the model's context.
//
// It is the third and optional injection path. The MCP `instructions` field and
// the `dusk_context` tool work without it, so nothing on the agent surface may
// assume a hook ran, and every failure here is silent.
//
// Nothing in this package decides what an agent is told. `dusk_context` ranks
// the answer and spends its budget server side (ADR-0050, ADR-0057), and what
// it returns is passed through unchanged.
package contexthook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolName is the tool a session is oriented by.
const ToolName = "dusk_context"

// Path is where the agent surface is served. An endpoint naming only a host is
// taken to mean this, because it is the only path the surface answers at.
const Path = "/mcp"

// DefaultTimeout bounds the whole exchange, so a Dusk that is slow to answer
// cannot be the reason a session is slow to start.
const DefaultTimeout = 5 * time.Second

// clientName identifies the hook to the server, which logs who connected.
const clientName = "dusk-context"

// ErrNotConfigured is returned when nothing says which Dusk to ask. Distinct,
// because a hook installed once fires in every repository and most of them have
// nothing to do with any catalog, which is ordinary rather than a failure.
var ErrNotConfigured = errors.New("no Dusk is configured")

// Options say which Dusk to ask and how long to wait for it.
type Options struct {
	// Endpoint is Dusk's MCP URL. One naming only a host is read as Path.
	Endpoint string

	// Token is the bearer token the agent surface requires. Empty is valid:
	// a deployment on a trusted network serves that surface unauthenticated.
	Token string

	// Timeout bounds the whole exchange. Zero means DefaultTimeout.
	Timeout time.Duration

	// Version is what this client reports itself as. Empty means "dev".
	Version string
}

// Fetch asks Dusk what an agent working in root should know, and returns what
// `dusk_context` rendered, unchanged. root is the exact owner/name repository
// resolved from the checkout; an empty one asks about the whole estate.
func Fetch(ctx context.Context, opts Options, root string) (string, error) {
	endpoint, err := normalize(opts.Endpoint)
	if err != nil {
		return "", err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	version := opts.Version
	if version == "" {
		version = "dev"
	}

	client := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: version}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: bearer{token: opts.Token}},

		// One question and one answer. A hook has no use for anything the
		// server might say later, and every reason not to hold a connection
		// open past the session start it is delaying.
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("connecting to %s: %w", endpoint, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      ToolName,
		Arguments: map[string]any{"root": root},
	})
	if err != nil {
		return "", fmt.Errorf("calling %s: %w", ToolName, err)
	}
	if result.IsError {
		return "", fmt.Errorf("%s refused: %s", ToolName, textOf(result))
	}

	body := textOf(result)
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("%s answered with no text", ToolName)
	}
	return body, nil
}

// normalize turns what an operator wrote down into the URL the surface is
// served at. A bare host is the likeliest thing to be configured, and asking
// the wrong URL would be indistinguishable from a Dusk that is not there.
func normalize(endpoint string) (string, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return "", ErrNotConfigured
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("reading %q as a URL: %w", trimmed, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%q is not an absolute URL, so there is no Dusk to ask", trimmed)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = Path
	}
	return parsed.String(), nil
}

// bearer presents the agent surface's token. The transport takes an HTTP client
// rather than a header map, so a round tripper is where an Authorization header
// can be set.
type bearer struct{ token string }

func (b bearer) RoundTrip(request *http.Request) (*http.Response, error) {
	if b.token == "" {
		return http.DefaultTransport.RoundTrip(request)
	}

	// A round tripper may not modify the request it was handed.
	authorized := request.Clone(request.Context())
	authorized.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(authorized)
}

// textOf is the text a tool answered with. Dusk answers in markdown rather than
// structured content, so everything worth injecting is in here.
func textOf(result *sdk.CallToolResult) string {
	var out strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	return out.String()
}
