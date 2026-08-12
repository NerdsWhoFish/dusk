// Command dusk runs the Dusk server and its local tools.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/FetchHQ/dusk/internal/config"
	"github.com/FetchHQ/dusk/internal/controller"
	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/internal/mcp"
	"github.com/FetchHQ/dusk/internal/server"
	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/internal/write"
	"github.com/FetchHQ/dusk/pkg/githubapp"
	"github.com/FetchHQ/dusk/pkg/proof"
	"github.com/FetchHQ/dusk/pkg/vault"
)

// version is set at build time with -ldflags.
var version = "dev"

func main() {
	if err := command().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func command() *cli.Command {
	return &cli.Command{
		Name:    "dusk",
		Usage:   "a service catalog that maintains itself",
		Version: version,

		// The image's entrypoint is the bare binary, so no arguments has to
		// mean serve. Printing help instead exits zero and crash loops.
		DefaultCommand: "serve",

		Commands: []*cli.Command{
			serveCommand(),
			validateCommand(),
			genkeyCommand(),
		},
	}
}

func serveCommand() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "run the server",
		Description: fmt.Sprintf(`Environment:
  DUSK_PRIVATE_HOST     Required. Where you reach the UI, and where GitHub
                        returns your browser during setup.
  DUSK_PUBLIC_HOST      Where GitHub delivers webhooks. Defaults to the private
                        host. Set it when a forwarder exposes only /webhooks.
  DUSK_ENCRYPTION_KEY   Required. Base64 32-byte key. Generate with 'dusk genkey'.
  DUSK_ADDR             Listen address (default %s)
  DUSK_DATA_DIR         Where credentials and the index live (default %s)
  DUSK_ALLOWED_ACCOUNTS Comma separated GitHub accounts whose installations may
                        be reconciled. Defaults to the account the App belongs
                        to. Anyone who can see an App can install it, so this is
                        what keeps an uninvited installation out of the catalog.
  DUSK_MCP_TOKEN        Bearer token the agent surface requires. Without it, and
                        without DUSK_TRUSTED_NETWORK, /mcp is off: one read
                        returns the whole catalog, so Dusk will not guess.
  DUSK_TRUSTED_NETWORK  Set to true to serve the agent surface with no
                        authentication at all, on a network you trust.`,
			config.DefaultAddr, config.DefaultDataDir),
		Action: func(ctx context.Context, _ *cli.Command) error { return serve(ctx) },
	}
}

func genkeyCommand() *cli.Command {
	return &cli.Command{
		Name:  "genkey",
		Usage: "generate a DUSK_ENCRYPTION_KEY",
		Action: func(_ context.Context, _ *cli.Command) error {
			key, err := vault.NewKey()
			if err != nil {
				return err
			}
			fmt.Println(key)
			return nil
		},
	}
}

func serve(parent context.Context) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	master, err := cfg.MasterKey()
	if err != nil {
		return err
	}
	credentials, err := store.New(cfg.DataDir, master)
	if err != nil {
		return err
	}

	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()

	catalog, err := controller.New(controller.Options{
		Index:       idx,
		Client:      &githubapp.Client{},
		Credentials: credentials,
		Accounts:    cfg.AllowedAccounts,
		Logger:      log,
	})
	if err != nil {
		return err
	}

	tokens := &proof.Store{}
	agents := mcp.New(mcp.Options{
		Catalog: idx,
		Syncs:   syncStatus{catalog},
		Version: version,
		Tokens:  tokens,
		Writer: &write.Writer{
			Catalog:      idx,
			Repositories: catalog,
			Proof:        tokens,
		},
	})
	agentSurface, agentMode := guard(agents.Handler(), cfg)

	srv, err := server.New(server.Options{
		Config:      cfg,
		Credentials: credentials,
		Controller:  catalog,
		MCP:         agentSurface,
		Logger:      log,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The poll floor runs whether or not webhooks are configured, and it is
	// what keeps a lost delivery from leaving the catalog quietly stale.
	go catalog.Run(ctx)

	errc := make(chan error, 1)
	go func() {
		announce(log, cfg, credentials.Configured(), agentMode)
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// syncStatus adapts the controller's status to what the MCP surface reports,
// so neither package has to know the other's shape.
type syncStatus struct{ controller *controller.Controller }

func (s syncStatus) Status() []mcp.SyncStatus {
	from := s.controller.Status()
	out := make([]mcp.SyncStatus, 0, len(from))
	for _, status := range from {
		out = append(out, mcp.SyncStatus{
			Repository: status.Repository,
			Commit:     status.Commit,
			Entities:   status.Entities,
			Relations:  status.Relations,
			Error:      status.Error,
		})
	}
	return out
}

// guard decides who may read the catalog, and reports which way it went so the
// answer appears in the boot log rather than only in the environment.
func guard(handler http.Handler, cfg *config.Config) (http.Handler, string) {
	switch {
	case !cfg.MCPToken.IsZero():
		return mcp.RequireBearer(handler, cfg.MCPToken), "token"
	case cfg.TrustedNetwork:
		return handler, "unauthenticated"
	default:
		return mcp.Disabled(), "off"
	}
}

// announce puts the decisions that are easy to get wrong into the boot log,
// where an operator will actually read them.
func announce(log *slog.Logger, cfg *config.Config, onboarded bool, agentMode string) {
	log.Info("dusk listening",
		"addr", cfg.Addr,
		"private_host", cfg.PrivateHost,
		"public_host", cfg.PublicHost,
		"webhook_url", cfg.WebhookURL(),
		"onboarded", onboarded,
		"agent_surface", agentMode,
		"version", version)

	switch agentMode {
	case "off":
		log.Warn("the agent surface is off: set DUSK_MCP_TOKEN, or DUSK_TRUSTED_NETWORK=true to serve it unauthenticated")
	case "unauthenticated":
		log.Warn("the agent surface is unauthenticated: anything that can reach this port can read the whole catalog")
	}
	if !onboarded {
		log.Info("not onboarded yet", "setup", cfg.PrivateHost+"/setup")
	}
}
