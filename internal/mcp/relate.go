package mcp

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/internal/write"
)

type relateInput struct {
	From  string `json:"from" jsonschema:"the entity the relation points from, of the form kind:namespace/name. Its file is what declares the relation, so the proof token has to come from a read of this one"`
	To    string `json:"to" jsonschema:"the entity it points at, of the form kind:namespace/name. It does not have to be in the catalog"`
	Type  string `json:"type" jsonschema:"what the relation is, such as runs_on, depends_on, backs_up_to or stores_in"`
	Proof string `json:"proof" jsonschema:"the proof token from the read that returned the entity it points from"`
}

// relate declares one outbound edge. Only outbound: the from side is the file's
// own entity, which is what keeps a repository from asserting facts about
// entities it does not own (ADR-0026).
func (s *Server) relate(ctx context.Context, _ *sdk.CallToolRequest, in relateInput) (*sdk.CallToolResult, any, error) {
	result, err := s.opts.Writer.Relate(ctx, in.Proof, write.Relation{
		From: in.From, To: in.To, Type: in.Type,
	})
	if err != nil {
		return text(fmt.Sprintf("The relation was not declared.\n\n%s", err)), nil, nil
	}
	if result.Proposed {
		return text(proposal(result)), nil, nil
	}
	if result.Existing {
		return text(fmt.Sprintf(
			"`%s` already says %s `%s`, so nothing was written.\n\nIt is declared in `%s` in %s.",
			result.Ref, in.Type, in.To, result.Path, result.Repository)), nil, nil
	}

	return text(fmt.Sprintf(
		"`%s` now says %s `%s`, declared in %s at `%s`.\n\nCommit: %s\n\n"+
			"The edge is written in the file of the entity it points from and nowhere else, "+
			"which is how a repository is kept from asserting facts about what it does not own. "+
			"`get` and `neighbors` on `%s` will still show it, because the graph is assembled across repositories. "+
			"It reaches the catalog on the next reconcile, which the push already triggered.%s",
		result.Ref, in.Type, in.To, result.Repository, result.Path, result.URL,
		in.To, s.warnAboutTarget(ctx, in.To))), nil, nil
}

// warnAboutTarget says the other end is not in the catalog. ADR-0033 reports
// rather than refuses, and a typo is indistinguishable from a repository Dusk
// has not been shown, so the one caller that can tell is told.
func (s *Server) warnAboutTarget(ctx context.Context, ref string) string {
	if _, err := s.opts.Catalog.Get(ctx, "", ref); !errors.Is(err, index.ErrNotFound) {
		return ""
	}
	return fmt.Sprintf("\n\nNothing in the catalog declares `%s`. That is not an error: "+
		"it may live in a repository Dusk has not been shown, and `drift` lists it either way. "+
		"If it was meant to be something already declared, `search` finds the right ref.", ref)
}
