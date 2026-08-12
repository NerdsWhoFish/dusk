package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/FetchHQ/dusk/internal/config"
	"github.com/FetchHQ/dusk/internal/index"
	"github.com/FetchHQ/dusk/internal/reconcile"
)

// ingestCommand loads a local checkout into the index without GitHub, so Dusk
// can be tried and the UI worked on before registering an App, which is the
// largest thing between cloning this and seeing whether you want it.
func ingestCommand() *cli.Command {
	return &cli.Command{
		Name:      "ingest",
		Usage:     "load a local repository into the index, with no GitHub App",
		ArgsUsage: "<dir>",
		Description: "Reads a checkout the way a reconcile would and stores the result,\n" +
			"so `dusk serve` has something to show. A real sweep replaces it.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "ref",
				Value: "refs/heads/main",
				Usage: "git ref to store the tree as",
			},
			&cli.StringFlag{
				Name:  "repository",
				Usage: "owner/name to attribute it to (default: the directory name)",
			},
		},
		Action: ingestDirectory,
	}
}

func ingestDirectory(ctx context.Context, cmd *cli.Command) error {
	dir := cmd.Args().First()
	if dir == "" {
		return fmt.Errorf("ingest: which directory? Pass one, for example `dusk ingest .`")
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}

	repository := cmd.String("repository")
	if repository == "" {
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		repository = "local/" + filepath.Base(absolute)
	}
	gitRef := cmd.String("ref")

	source, err := reconcile.NewDir(dir, gitRef)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()

	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()

	graph, err := reconcile.New(source, idx).Reconcile(ctx, repository, gitRef, time.Now())
	if err != nil {
		return err
	}
	if !graph.Participating {
		return fmt.Errorf("ingest: %s has no %s, so there is nothing to load", dir, reconcile.RootFile)
	}

	// Without a default view a query with no ref finds nothing, and the UI
	// asks with no ref.
	if err := idx.SetDefaultView(ctx, repository, gitRef); err != nil {
		return err
	}

	fmt.Printf("ingested %s as %s: %d entities, %d relations, %d notes\n",
		dir, repository, len(graph.Entities), len(graph.Relations), len(graph.Notes))
	return nil
}
