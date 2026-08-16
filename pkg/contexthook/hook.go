package contexthook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Event is the Claude Code hook event this is installed on. SessionStart is one
// of three whose output reaches the model's context; on any other, what a hook
// prints goes to a debug log and the agent never sees it.
const Event = "SessionStart"

// Environment variables the hook is configured from. The token carries the same
// name the server requires it under, because it is the same secret.
const (
	EndpointVar = "DUSK_MCP_URL"
	TokenVar    = "DUSK_MCP_TOKEN"
)

// OptionsFromEnv reads the hook's configuration from the environment, and from
// nowhere else: a hook is installed by an entry in .claude/settings.json, which
// is committed, so a token written there is a token in somebody's git history.
func OptionsFromEnv() Options {
	return Options{
		Endpoint: os.Getenv(EndpointVar),
		Token:    os.Getenv(TokenVar),
	}
}

// payload is the hook invocation the client writes on standard input. One field
// is read, because the rest of that schema is the moving target ADR-0014
// accepted and nothing here needs any of it.
type payload struct {
	CWD string `json:"cwd"`
}

// injection is what the client reads back. The documented JSON form is used
// rather than bare stdout, which reaches the context only because SessionStart
// is special and is parsed as JSON whenever it happens to open with a brace.
type injection struct {
	HookSpecificOutput hookOutput `json:"hookSpecificOutput"`
}

type hookOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// Run reads the hook payload from in, asks Dusk about the directory the session
// is in, and writes what the client injects to out. Failures are silent and go
// only to diag: a hook that errors where Dusk is irrelevant is worse than none.
func Run(ctx context.Context, opts Options, in io.Reader, out, diag io.Writer) {
	body, err := Fetch(ctx, opts, rootOf(in, diag))
	if err != nil {
		say(diag, "nothing injected: %v", err)
		return
	}

	encoded, err := json.Marshal(injection{HookSpecificOutput: hookOutput{
		HookEventName:     Event,
		AdditionalContext: body,
	}})
	if err != nil {
		say(diag, "nothing injected: %v", err)
		return
	}
	if _, err := fmt.Fprintln(out, string(encoded)); err != nil {
		say(diag, "writing the answer: %v", err)
	}
}

// PayloadFrom reports where a hook payload can be read from, or nil when there
// is none. A terminal on standard input is somebody running this by hand, and
// reading it would block forever waiting for a payload nobody will type.
func PayloadFrom(stdin *os.File) io.Reader {
	info, err := stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return stdin
}

// rootOf is the directory the session is in. The payload wins, because a client
// may run a hook from somewhere other than the directory the session is about.
// The process's own is what makes a hand run answer a session's question.
func rootOf(in io.Reader, diag io.Writer) string {
	if in != nil {
		var decoded payload
		// An absent, empty or unreadable payload is a hand run, not a failure.
		if err := json.NewDecoder(in).Decode(&decoded); err == nil && decoded.CWD != "" {
			return decoded.CWD
		}
	}

	working, err := os.Getwd()
	if err != nil {
		say(diag, "no working directory, asking about the whole estate: %v", err)
		return ""
	}
	return working
}

// say writes one diagnostic. It is flattened onto a single line here rather
// than by whatever produced the message, because a client renders a hook's
// complaint as its first line and the rest would be dropped without notice.
func say(diag io.Writer, format string, args ...any) {
	message := fmt.Sprintf(clientName+": "+format, args...)
	_, _ = fmt.Fprintln(diag, strings.Join(strings.Fields(message), " "))
}
