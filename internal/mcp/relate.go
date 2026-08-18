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
	From       string            `json:"from" jsonschema:"the entity the relation points from, of the form kind:namespace/name. Its file is what declares the relation, so the proof token has to come from a read of this one"`
	To         string            `json:"to" jsonschema:"the entity it points at, of the form kind:namespace/name. It does not have to be in the catalog"`
	Type       string            `json:"type" jsonschema:"what the relation is, such as runs_on, depends_on, backs_up_to or stores_in"`
	Proof      string            `json:"proof" jsonschema:"the proof token from the read that returned the entity it points from"`
	Repository string            `json:"repository,omitempty" jsonschema:"the exact owner/name repository whose edge to change when the source ref is declared more than once"`
	Attributes map[string]string `json:"attributes,omitempty" jsonschema:"attributes to set on the edge, merged with what is there"`
	Unset      []string          `json:"unset,omitempty" jsonschema:"relation attribute names to remove"`
	Remove     bool              `json:"remove,omitempty" jsonschema:"withdraw this exact edge"`
	Confirm    bool              `json:"confirm,omitempty" jsonschema:"required with remove because the connection disappears from the graph"`
}

// relate declares one outbound edge. Only outbound: the from side is the file's
// own entity, which is what keeps a repository from asserting facts about
// entities it does not own (ADR-0026).
func (s *Server) relate(ctx context.Context, _ *sdk.CallToolRequest, in relateInput) (*sdk.CallToolResult, any, error) {
	result, err := s.opts.Writer.Relate(ctx, in.Proof, write.Relation{
		From: in.From, To: in.To, Type: in.Type, Repository: in.Repository,
		Attributes: in.Attributes, Unset: in.Unset, Remove: in.Remove, Confirm: in.Confirm,
	})
	if err != nil {
		return failure("relation_write_failed", fmt.Errorf("the relation was not declared: %w", err)), nil, nil
	}
	if result.Proposed {
		return success(proposal(result), result), nil, nil
	}
	if result.Existing {
		state := "already declares"
		if in.Remove {
			state = "already does not declare"
		}
		return success(fmt.Sprintf(
			"`%s` %s %s `%s`, so nothing was written.\n\nThe declaration file is `%s` in %s.",
			result.Ref, state, in.Type, in.To, result.Path, result.Repository), result), nil, nil
	}
	if in.Remove {
		return success(fmt.Sprintf(
			"`%s` no longer says %s `%s`; the edge was withdrawn from %s at `%s`.\n\nCommit: %s\n\nIt leaves the catalog on the next reconcile, which the push already triggered.",
			result.Ref, in.Type, in.To, result.Repository, result.Path, result.URL), result), nil, nil
	}

	return success(fmt.Sprintf(
		"`%s` now says %s `%s`, declared in %s at `%s`.\n\nCommit: %s\n\n"+
			"That file is the only place the edge is written, and `get` on `%s` will show it anyway, "+
			"because the graph is assembled across repositories. "+
			"It reaches the catalog on the next reconcile, which the push already triggered.%s",
		result.Ref, in.Type, in.To, result.Repository, result.Path, result.URL,
		in.To, s.warnAboutTarget(ctx, in.To)), result), nil, nil
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
