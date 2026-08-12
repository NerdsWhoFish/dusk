package mcp

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FetchHQ/dusk/internal/index"
)

// ContextBudget caps the rendered context in bytes, standing in for ADR-0014's
// token ceiling. Pinning is free to whoever pins and costs every future
// session, so the limit is enforced rather than advised.
const ContextBudget = 8000

type contextInput struct {
	Root string `json:"root,omitempty" jsonschema:"the repository being worked in, as a path or owner/name. Omit for an inventory of everything"`
}

// duskContext tailors the catalog to the repository an agent is working in.
// ADR-0014 makes it a tool rather than only an instructions block, so every
// client can reach it and a hook is an accelerator rather than a requirement.
func (s *Server) duskContext(ctx context.Context, _ *sdk.CallToolRequest, in contextInput) (*sdk.CallToolResult, any, error) {
	var out strings.Builder

	repository, err := s.matchRepository(ctx, in.Root)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case in.Root == "":
		out.WriteString("# This operator's catalog\n\n")
	case repository == "":
		fmt.Fprintf(&out, "# %s is not in the catalog\n\n", displayRoot(in.Root))
		out.WriteString("Nothing here declares a `dusk.md`, so Dusk knows nothing about this repository.\n" +
			"Adding one at the root opts it in, and is how a repository joins the catalog.\n\n")
	default:
		fmt.Fprintf(&out, "# %s in the catalog\n\n", repository)
	}

	if repository != "" {
		if err := s.renderOwned(ctx, &out, repository); err != nil {
			return nil, nil, err
		}
	}

	if err := s.renderInventory(ctx, &out); err != nil {
		return nil, nil, err
	}
	if err := s.renderDriftSummary(ctx, &out); err != nil {
		return nil, nil, err
	}

	out.WriteString("\nAsk `search` before assuming something is not here. " +
		"The catalog only knows what a repository wrote down, so absence means nobody documented it, not that it does not exist.\n")

	return text(truncate(out.String(), ContextBudget)), nil, nil
}

// matchRepository maps a filesystem root or slug to a repository the catalog
// knows. Matching on the trailing owner/name is what lets an agent pass the
// absolute path it actually has.
func (s *Server) matchRepository(ctx context.Context, root string) (string, error) {
	if root == "" {
		return "", nil
	}
	if s.opts.Catalog == nil {
		return "", nil
	}

	scopes, err := s.opts.Catalog.Scopes(ctx)
	if err != nil {
		return "", err
	}

	cleaned := strings.Trim(path.Clean(root), "/")
	for _, scope := range scopes {
		if index.IsObserved(scope.Repository) {
			continue
		}
		if cleaned == scope.Repository || strings.HasSuffix(cleaned, "/"+scope.Repository) {
			return scope.Repository, nil
		}
	}
	return "", nil
}

func (s *Server) renderOwned(ctx context.Context, out *strings.Builder, repository string) error {
	entities, err := s.opts.Catalog.List(ctx, "", "")
	if err != nil {
		return err
	}

	var owned []string
	for _, entity := range entities {
		if entity.GetProvenance().GetSource() != "" && strings.Contains(entity.GetProvenance().GetSource(), repository) {
			owned = append(owned, entity.GetRef())
		}
	}

	fmt.Fprintf(out, "This repository declares %d entit%s.\n\n", len(owned), plural(len(owned)))
	for _, ref := range owned {
		fmt.Fprintf(out, "- `%s`\n", ref)
	}
	out.WriteString("\n")
	return nil
}

// renderInventory is names only, per ADR-0014: this is an interaction manual
// and an inventory, not somebody's whole CLAUDE.md.
func (s *Server) renderInventory(ctx context.Context, out *strings.Builder) error {
	entities, err := s.opts.Catalog.List(ctx, "", "")
	if err != nil {
		return err
	}
	if len(entities) == 0 {
		out.WriteString("The catalog is empty.\n")
		return nil
	}

	byKind := map[string][]string{}
	for _, entity := range entities {
		byKind[entity.GetKind()] = append(byKind[entity.GetKind()], entity.GetRef())
	}

	fmt.Fprintf(out, "## What this operator has (%d)\n\n", len(entities))
	for _, kind := range sortedKeysOf(byKind) {
		refs := byKind[kind]
		fmt.Fprintf(out, "**%s** (%d): %s\n\n", kind, len(refs), strings.Join(refs, ", "))
	}
	return nil
}

// renderDriftSummary is a count rather than a list. The detail is a `drift`
// call away, and spending the budget on it would crowd out the inventory.
func (s *Server) renderDriftSummary(ctx context.Context, out *strings.Builder) error {
	drifts, err := s.opts.Catalog.Drift(ctx, "")
	if err != nil {
		return err
	}
	if len(drifts) == 0 {
		return nil
	}

	var undeclared int
	for _, drift := range drifts {
		if drift.Kind == index.DriftUndeclared {
			undeclared++
		}
	}
	fmt.Fprintf(out, "\n%d thing(s) have drifted, %d of them running and undeclared. Call `drift` for the list.\n",
		len(drifts), undeclared)
	return nil
}

// truncate cuts at the budget and says so. A silently shortened context
// degrades every answer with nothing to connect the degradation to.
func truncate(body string, budget int) string {
	if len(body) <= budget {
		return body
	}

	cut := body[:budget]
	if last := strings.LastIndex(cut, "\n"); last > 0 {
		cut = cut[:last]
	}
	return cut + fmt.Sprintf("\n\n---\nTruncated at %d bytes. Use `search` and `get` for anything not listed above.\n", budget)
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func sortedKeysOf(byKind map[string][]string) []string {
	return slices.Sorted(maps.Keys(byKind))
}

func displayRoot(root string) string {
	cleaned := strings.Trim(path.Clean(root), "/")
	if cleaned == "" {
		return root
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return cleaned
}
