package contexthook_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk/pkg/contexthook"
)

// injected is the shape the client reads back, spelled out here rather than
// reused from the package, so a change to what is written is a failing test
// rather than two definitions agreeing with each other.
type injected struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// run performs one hook invocation and returns what each stream received.
func run(t *testing.T, options contexthook.Options, payload string) (stdout, stderr string) {
	t.Helper()

	options.Timeout = 5 * time.Second
	var out, diag bytes.Buffer

	// A nil reader is a hand run: there is no payload at all, which is not the
	// same as an empty one.
	var in io.Reader
	if payload != "" {
		in = strings.NewReader(payload)
	}

	contexthook.Run(t.Context(), options, in, &out, &diag)
	return out.String(), diag.String()
}

// The hook adds nothing to what Dusk answered. The budget, the ranking and what
// is dropped are decided server side (ADR-0050, ADR-0057), so a client that
// edits the answer is a second policy nobody can see.
func TestADR0014_TheHookInjectsWhatDuskAnswered(t *testing.T) {
	dusk := &stub{answer: orientation}

	stdout, stderr := run(t, contexthook.Options{Endpoint: dusk.serve(t)},
		`{"session_id":"abc","hook_event_name":"SessionStart","cwd":"/src/example/homelab"}`)

	if stderr != "" {
		t.Errorf("a hook that worked said %q on stderr, want nothing", stderr)
	}

	var got injected
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("the client could not read %q: %v", stdout, err)
	}
	if got.HookSpecificOutput.HookEventName != contexthook.Event {
		t.Errorf("hookEventName = %q, want %q",
			got.HookSpecificOutput.HookEventName, contexthook.Event)
	}
	if got.HookSpecificOutput.AdditionalContext != orientation {
		t.Errorf("additionalContext =\n%s\nwant:\n%s",
			got.HookSpecificOutput.AdditionalContext, orientation)
	}
	if asked := dusk.asked(); len(asked) != 1 || asked[0] != "/src/example/homelab" {
		t.Errorf("dusk_context was asked about %v, want [/src/example/homelab]", asked)
	}
}

// Nothing on the agent surface may assume the hook ran, so a hook that cannot
// ask starts the session with no context and no error. Anything else makes a
// repository with nothing to do with Dusk worse for having it installed.
func TestADR0014_AHookThatCannotAskDuskIsSilent(t *testing.T) {
	unauthenticated := &stub{answer: orientation, token: "sesame"}
	refusing := &stub{refuses: true}
	verbose := &stub{refuses: true, refusal: "the catalog\ncould not\nbe read"}

	tests := []struct {
		name    string
		options contexthook.Options
	}{
		{name: "no Dusk is configured", options: contexthook.Options{}},
		{name: "the endpoint is not a URL", options: contexthook.Options{Endpoint: "dusk.example.com"}},
		{name: "Dusk is unreachable", options: contexthook.Options{Endpoint: unreachable(t)}},
		{name: "no token where one is required", options: contexthook.Options{Endpoint: unauthenticated.serve(t)}},
		{name: "the wrong token", options: contexthook.Options{Endpoint: unauthenticated.serve(t), Token: "guess"}},
		{name: "the tool refused", options: contexthook.Options{Endpoint: refusing.serve(t)}},
		{name: "the tool refused at length", options: contexthook.Options{Endpoint: verbose.serve(t)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr := run(t, test.options,
				`{"hook_event_name":"SessionStart","cwd":"/src/example/homelab"}`)

			if stdout != "" {
				t.Errorf("a failed hook wrote %q, want nothing injected", stdout)
			}
			if !strings.HasPrefix(stderr, "dusk-context: nothing injected: ") {
				t.Errorf("stderr = %q, want it to say why nothing was injected", stderr)
			}
			if strings.Count(stderr, "\n") != 1 {
				t.Errorf("stderr = %q, want one line: only the first reaches a hook error", stderr)
			}
		})
	}
}

func TestRunTakesTheDirectoryFromThePayload(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)

	// The path a session is in after the shell and the OS have both had their
	// say, which is what has to come back rather than what TempDir returned.
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "the payload names it",
			payload: `{"cwd":"/src/example/homelab"}`,
			want:    "/src/example/homelab",
		},
		{
			name:    "no payload, so a hand run",
			payload: "",
			want:    resolved,
		},
		{
			name:    "a payload naming no directory",
			payload: `{"session_id":"abc"}`,
			want:    resolved,
		},
		{
			name:    "a payload that is not JSON at all",
			payload: "who knows what a client sends next",
			want:    resolved,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dusk := &stub{answer: orientation}

			stdout, stderr := run(t, contexthook.Options{Endpoint: dusk.serve(t)}, test.payload)
			if stdout == "" {
				t.Fatalf("nothing was injected, stderr: %s", stderr)
			}

			if asked := dusk.asked(); len(asked) != 1 || asked[0] != test.want {
				t.Errorf("dusk_context was asked about %v, want [%s]", asked, test.want)
			}
		})
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv(contexthook.EndpointVar, "https://dusk.example.com/mcp")
	t.Setenv(contexthook.TokenVar, "sesame")

	options := contexthook.OptionsFromEnv()
	if options.Endpoint != "https://dusk.example.com/mcp" {
		t.Errorf("Endpoint = %q, want the configured URL", options.Endpoint)
	}
	if options.Token != "sesame" {
		t.Errorf("Token = %q, want the configured token", options.Token)
	}
}

func TestOptionsFromEnvWithNothingSet(t *testing.T) {
	t.Setenv(contexthook.EndpointVar, "")
	t.Setenv(contexthook.TokenVar, "")

	if options := contexthook.OptionsFromEnv(); options.Endpoint != "" || options.Token != "" {
		t.Errorf("OptionsFromEnv = %+v, want nothing configured", options)
	}
}

// Reading a terminal would hang a hand run forever waiting for a payload nobody
// is going to type, which is the one failure a hook cannot recover from.
func TestPayloadFrom(t *testing.T) {
	regular, err := os.Create(filepath.Join(t.TempDir(), "payload.json"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = regular.Close() })

	readable, writable, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	t.Cleanup(func() { _, _ = readable.Close(), writable.Close() })

	terminal, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = terminal.Close() })

	tests := []struct {
		name string
		file *os.File
		want bool
	}{
		{name: "a pipe, which is how a client sends one", file: readable, want: true},
		{name: "a file, which is how a person replays one", file: regular, want: true},
		{name: "a character device, which is a terminal", file: terminal, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := contexthook.PayloadFrom(test.file) != nil; got != test.want {
				t.Errorf("PayloadFrom gave a payload = %v, want %v", got, test.want)
			}
		})
	}
}
