package main

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/FetchHQ/dusk/internal/reconcile"
)

// validateCommand checks a checkout with no server, index, or network. It reads
// exactly what a reconcile reads, so passing is a real answer to "will this
// repository load" rather than an approximation of one.
func validateCommand() *cli.Command {
	return &cli.Command{
		Name:      "validate",
		Usage:     "check a local repository's dusk.md files",
		ArgsUsage: "[dir]",
		Description: "Reads the root " + reconcile.RootFile + " and everything it includes,\n" +
			"reporting what the catalog would contain and every problem found.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "ref",
				Value: "refs/heads/main",
				Usage: "git ref to report the tree as",
			},
		},
		Action: validate,
	}
}

func validate(ctx context.Context, cmd *cli.Command) error {
	dir := cmd.Args().First()
	if dir == "" {
		dir = "."
	}
	gitRef := cmd.String("ref")

	source, err := reconcile.NewDir(dir, gitRef)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()

	graph, err := reconcile.NewLoader(source).Load(ctx, gitRef, time.Now())
	if err != nil {
		return err
	}

	if !graph.Participating {
		fmt.Printf("%s has no %s, so it is not in the catalog\n", dir, reconcile.RootFile)
		return nil
	}

	width := 0
	for _, file := range graph.Files {
		width = max(width, len(file))
	}
	for i, file := range graph.Files {
		fmt.Printf("  %-*s  %s\n", width, file, graph.Entities[i].GetRef())
	}
	fmt.Printf("\n%s is valid: %d entities, %d relations, from %d files\n",
		dir, len(graph.Entities), len(graph.Relations), len(graph.Files))
	return nil
}
