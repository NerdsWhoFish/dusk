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
	"github.com/FetchHQ/dusk/internal/server"
	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/pkg/githubapp"
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
                        what keeps an uninvited installation out of the catalog.`,
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

	srv, err := server.New(server.Options{
		Config:      cfg,
		Credentials: credentials,
		Controller:  catalog,
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
		log.Info("dusk listening",
			"addr", cfg.Addr,
			"private_host", cfg.PrivateHost,
			"public_host", cfg.PublicHost,
			"webhook_url", cfg.WebhookURL(),
			"onboarded", credentials.Configured(),
			"version", version)
		if !credentials.Configured() {
			log.Info("not onboarded yet", "setup", cfg.PrivateHost+"/setup")
		}
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
