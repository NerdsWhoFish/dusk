// Command dusk runs the Dusk server.
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

	"github.com/FetchHQ/dusk/internal/config"
	"github.com/FetchHQ/dusk/internal/server"
	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/pkg/vault"
)

// version is set at build time with -ldflags.
var version = "dev"

const usage = `dusk - a service catalog that maintains itself

Usage:
  dusk serve     Run the server
  dusk genkey    Generate a DUSK_ENCRYPTION_KEY
  dusk version   Print the version

Environment:
  DUSK_EXTERNAL_URL     Required. Base URL browsers and GitHub reach Dusk at.
  DUSK_ENCRYPTION_KEY   Required. Base64 32-byte key. Generate with 'dusk genkey'.
  DUSK_ADDR             Listen address (default %s)
  DUSK_DATA_DIR         Where credentials live (default %s)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "serve":
		return serve()
	case "genkey":
		return genkey()
	case "version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		fmt.Printf(usage, config.DefaultAddr, config.DefaultDataDir)
		return nil
	default:
		fmt.Printf(usage, config.DefaultAddr, config.DefaultDataDir)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func genkey() error {
	key, err := vault.NewKey()
	if err != nil {
		return err
	}
	fmt.Println(key)
	return nil
}

func serve() error {
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

	srv, err := server.New(server.Options{Config: cfg, Credentials: credentials, Logger: log})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Info("dusk listening",
			"addr", cfg.Addr,
			"external_url", cfg.ExternalURL,
			"onboarded", credentials.Configured(),
			"version", version)
		if !credentials.Configured() {
			log.Info("not onboarded yet", "setup", cfg.ExternalURL+"/setup")
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
