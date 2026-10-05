package contexthook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Event is the Claude Code hook event this is installed on. SessionStart is one
// of three whose output reaches the model's context; on any other, what a hook
// prints goes to a debug log and the agent never sees it.
const Event = "SessionStart"

// Claude Code replaces oversized strings with a file preview. Counting bytes
// conservatively stays below its character ceiling without truncating policy.
const injectionLimit = 10000

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

// payload reads only the directory and conversation lifecycle from the client.
type payload struct {
	CWD    string `json:"cwd"`
	Source string `json:"source"`
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
	invocation := invocationOf(in, diag)
	// These events retain conversation history. The agent explicitly reloads
	// startup if policy was lost; a transport or lifecycle event cannot tell us.
	switch invocation.Source {
	case "resume", "compact", "fork":
		return
	}
	root := repositoryOf(ctx, invocation.CWD, diag)
	body, err := Fetch(ctx, opts, root)
	if err != nil {
		say(diag, "nothing injected: %v", err)
		return
	}
	if len(body) > injectionLimit {
		args, err := json.Marshal(map[string]string{"root": root, "mode": "startup"})
		if err != nil {
			say(diag, "nothing injected: %v", err)
			return
		}
		body = "Dusk startup was not injected because it exceeds the hook output limit. Before acting, call dusk_context(" + string(args) + ") and read the complete startup policy and applicable pinned notes. This message is not the startup payload."
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

var githubRemote = regexp.MustCompile(`(?i)(?:github\.com[:/])([^/]+)/([^/]+?)(?:\.git)?$`)

// repositoryOf asks Git for the checkout's configured origin instead of
// guessing from directory names. A non-GitHub checkout falls back to its path,
// which Dusk will correctly report as unknown rather than suffix-matching it.
func repositoryOf(ctx context.Context, root string, diag io.Writer) string {
	if root == "" {
		return ""
	}
	output, err := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return root
	}
	remote := strings.TrimSpace(string(output))
	match := githubRemote.FindStringSubmatch(remote)
	if len(match) != 3 {
		say(diag, "origin %q is not a GitHub repository", remote)
		return root
	}
	return match[1] + "/" + match[2]
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

// invocationOf resolves the directory the session is in. The payload wins, because a client
// may run a hook from somewhere other than the directory the session is about.
// The process's own is what makes a hand run answer a session's question.
func invocationOf(in io.Reader, diag io.Writer) payload {
	var decoded payload
	if in != nil {
		// An absent, empty or unreadable payload is a hand run, not a failure.
		if err := json.NewDecoder(in).Decode(&decoded); err != nil {
			decoded = payload{}
		}
	}
	if decoded.CWD != "" {
		return decoded
	}

	working, err := os.Getwd()
	if err != nil {
		say(diag, "no working directory, asking about the whole estate: %v", err)
		return decoded
	}
	decoded.CWD = working
	return decoded
}

// say writes one diagnostic. It is flattened onto a single line here rather
// than by whatever produced the message, because a client renders a hook's
// complaint as its first line and the rest would be dropped without notice.
func say(diag io.Writer, format string, args ...any) {
	message := fmt.Sprintf(clientName+": "+format, args...)
	_, _ = fmt.Fprintln(diag, strings.Join(strings.Fields(message), " "))
}
