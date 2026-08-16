// Command dusk-context injects Dusk's orientation into an agent session.
//
// It is ADR-0014's client hook: Claude Code runs it on SessionStart, it asks
// dusk_context about the directory the session is in, and it prints what the
// client adds to the model's context. Run it by hand in a repository to see
// what a session there would be given, and to read on stderr why it is quiet.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/NerdsWhoFish/dusk/pkg/contexthook"
)

// version is set at build time with -ldflags, and is what the hook reports
// itself to Dusk as.
var version = "dev"

func main() {
	timeout := flag.Duration("timeout", contexthook.DefaultTimeout,
		"how long to wait for Dusk before starting the session without it")
	flag.Usage = usage
	flag.Parse()

	options := contexthook.OptionsFromEnv()
	options.Timeout = *timeout
	options.Version = version

	contexthook.Run(context.Background(), options,
		contexthook.PayloadFrom(os.Stdin), os.Stdout, os.Stderr)
}

func usage() {
	out := flag.CommandLine.Output()
	_, _ = fmt.Fprintf(out, `dusk-context %s: what an agent should know before it starts.

Reads a Claude Code SessionStart hook payload on standard input and prints the
context to inject. With a terminal on standard input it asks about the working
directory instead, which is how to see what a session here would be given.

It exits zero and prints nothing when there is no Dusk to ask, saying why on
standard error. Nothing depends on it having run.

Environment:
  %s   Dusk's MCP URL, such as https://dusk.example.com/mcp. A URL naming
                 only a host is read as %s on it. Unset means this does nothing.
  %s Bearer token for the agent surface, the same secret the server
                 requires under this name. Unset is right for a deployment
                 serving that surface on a trusted network.

Install it by adding a SessionStart hook to your Claude Code settings:

  {
    "hooks": {
      "SessionStart": [
        { "hooks": [ { "type": "command", "command": "dusk-context" } ] }
      ]
    }
  }

Set the environment where Claude Code will inherit it. Settings files are
committed, so a token does not belong in one.

Flags:
`, version, contexthook.EndpointVar, contexthook.Path, contexthook.TokenVar)
	flag.PrintDefaults()
}
