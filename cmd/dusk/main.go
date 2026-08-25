// Command dusk runs the Dusk server and its local tools.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/NerdsWhoFish/dusk/internal/ingest"

	"github.com/NerdsWhoFish/dusk/internal/answer"
	"github.com/NerdsWhoFish/dusk/internal/config"
	"github.com/NerdsWhoFish/dusk/internal/controller"
	"github.com/NerdsWhoFish/dusk/internal/events"
	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/mcp"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/internal/server"
	"github.com/NerdsWhoFish/dusk/internal/store"
	"github.com/NerdsWhoFish/dusk/internal/telemetry"
	"github.com/NerdsWhoFish/dusk/internal/write"
	"github.com/NerdsWhoFish/dusk/pkg/githubapp"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vault"
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
		Usage:   "the internal developer platform for homelabbers",
		Version: version,

		// The image's entrypoint is the bare binary, so no arguments has to
		// mean serve. Printing help instead exits zero and crash loops.
		DefaultCommand: "serve",

		Commands: []*cli.Command{
			serveCommand(),
			validateCommand(),
			ingestCommand(),
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
                        authentication at all, on a network you trust.
  DUSK_CONFIG_REPOSITORY
                        owner/name of the repository notes are written to. It
                        needs its own dusk.md, like any other. Unset means a
                        note cannot be written; reading them still works.
  DUSK_GITHUB_CLIENT_ID, DUSK_GITHUB_CLIENT_SECRET
                        An OAuth app, enabling sign-in with GitHub. A viewer
                        then sees only what the repositories they can read
                        declared. The bearer token still sees everything.
  DUSK_OBSERVED_VISIBLE_TO_ALL
                        Set to true to show entities no repository backs to
                        every signed-in viewer. They have no natural access
                        control, so they are hidden unless you say this.
  DUSK_AI_BASE_URL      Optional OpenAI-compatible API base URL, including /v1.
  DUSK_AI_API_KEY       Provider credential. Required with DUSK_AI_BASE_URL.
  DUSK_AI_MODELS        Comma-separated model allowlist shown in search.
  DUSK_AI_DEFAULT_MODEL Deployment default; the first allowed model when unset.`,
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
	log := slog.New(telemetry.LogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	shutdownTelemetry, err := telemetry.Start(parent, "dusk", version, log)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	defer stopTelemetry(parent, log, shutdownTelemetry)

	return run(parent, log)
}

func run(parent context.Context, log *slog.Logger) error {
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

	github := &githubapp.Client{HTTP: telemetry.HTTPClient(30 * time.Second)}
	catalog, err := controller.New(controller.Options{
		Index:       idx,
		Client:      github,
		Credentials: credentials,
		Accounts:    cfg.AllowedAccounts,
		PrivateHost: cfg.PrivateHost,
		Logger:      log,
	})
	if err != nil {
		return err
	}

	// Empty: plugins are the only observers now, and they join as they start
	// (ADR-0040).
	observers := ingest.NewScheduler(idx, log, time.Now)

	tokens := &proof.Store{TTL: cfg.ProofTTL}
	ran, err := events.Open(filepath.Join(cfg.DataDir, "actions.json"), log)
	if err != nil {
		return err
	}

	plugins := &plugin.Manager{
		Store:   &plugin.Store{Dir: filepath.Join(cfg.DataDir, "plugins"), Master: master},
		Market:  &plugin.Market{Orgs: cfg.PluginOrgs, Token: appToken(credentials, github), HTTP: telemetry.HTTPClient(30 * time.Second)},
		Rota:    observers,
		Log:     log,
		Catalog: idx,
		Proof:   tokens,
		Events:  ran,
	}

	writer := &write.Writer{
		Catalog:          idx,
		Repositories:     catalog,
		Access:           catalog,
		Proof:            tokens,
		ConfigRepository: cfg.ConfigRepository,
	}
	agents := mcp.New(mcp.Options{
		Catalog:        idx,
		Syncs:          syncStatus{catalog},
		Version:        version,
		Tokens:         tokens,
		Writer:         writer,
		Plugins:        plugins,
		SessionTimeout: cfg.MCPSessionTimeout,
	})
	agentSurface, agentMode := guard(agents.Handler(), cfg)

	srv, err := server.New(server.Options{
		Config:      cfg,
		Credentials: credentials,
		Controller:  catalog,
		Catalog:     idx,
		Syncs:       catalog,
		Pages:       writer,
		Notes:       writer,
		Plugins:     plugins,
		Rotation:    observers,
		Events:      ran,
		Answers:     answersFor(cfg, idx),
		Tokens:      tokens,
		MCP:         agentSurface,
		Logger:      log,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           telemetry.HTTPHandler(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The poll floor runs whether or not webhooks are configured, and it is
	// what keeps a lost delivery from leaving the catalog quietly stale.
	go catalog.Run(ctx)

	// Before anything reaches for GitHub, so an offline Dusk still comes up
	// with every plugin it had (ADR-0042).
	plugins.Restore(ctx)
	defer plugins.Stop()

	if observers.Any() {
		go observers.Start(ctx)
	}

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

func answersFor(cfg *config.Config, catalog answer.Catalog) *answer.Service {
	if !cfg.AI.Enabled() {
		return nil
	}
	providerURL, _ := url.Parse(cfg.AI.BaseURL)
	return &answer.Service{
		Catalog: catalog,
		Completer: &answer.OpenAI{
			BaseURL: cfg.AI.BaseURL,
			APIKey:  cfg.AI.APIKey,
		},
		Models:       cfg.AI.Models,
		DefaultModel: cfg.AI.DefaultModel,
		Provider:     providerURL.Host,
	}
}

func stopTelemetry(parent context.Context, log *slog.Logger, shutdown telemetry.Shutdown) {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		log.Error("shut down telemetry", "error", err)
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
			At:         status.At,
			Attempted:  status.Attempted,
		})
	}
	return out
}

func (s syncStatus) NextSweep() time.Time { return s.controller.NextSweep() }

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

	if cfg.ConfigRepository == "" {
		log.Info("no config repository: agents can declare entities but cannot write notes",
			"set", "DUSK_CONFIG_REPOSITORY")
	} else {
		log.Info("notes are written to the config repository", "repository", cfg.ConfigRepository)
	}
	if !onboarded {
		log.Info("not onboarded yet", "setup", cfg.PrivateHost+"/setup")
	}
}

// appToken mints an installation token for the marketplace, so listing plugins
// costs GitHub's authenticated budget rather than the 60 anonymous requests an
// hour that one page refresh can exhaust.
//
// Any installation will do. The marketplace reads public repositories, and what
// raises the limit is being somebody rather than being permitted.
func appToken(credentials *store.Store, client *githubapp.Client) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		saved, err := credentials.Load()
		if err != nil {
			return "", err
		}

		app := githubapp.App{ID: saved.AppID, PrivateKey: saved.PrivateKey}
		installations, err := client.Installations(ctx, app)
		if err != nil {
			return "", err
		}
		if len(installations) == 0 {
			return "", fmt.Errorf("the App is installed nowhere, so there is no token to mint")
		}

		token, err := client.InstallationToken(ctx, app, installations[0].ID)
		if err != nil {
			return "", err
		}
		return token.Token.Reveal(), nil
	}
}
