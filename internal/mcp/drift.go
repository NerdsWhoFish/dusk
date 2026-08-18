package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/internal/index"
)

// sortDrift splits one list into the three questions it answers, which read
// differently enough that one list of them is worse than three.
func sortDrift(drifts []index.Drift) (missing, notes, undeclared []index.Drift) {
	for _, drift := range drifts {
		switch drift.Kind {
		case index.DriftMissing:
			missing = append(missing, drift)
		case index.DriftNoteRef:
			notes = append(notes, drift)
		default:
			undeclared = append(undeclared, drift)
		}
	}
	return missing, notes, undeclared
}

type driftInput struct {
	Undeclared bool `json:"undeclared,omitempty" jsonschema:"also list what is running and written down nowhere. Off by default: that is a description of reality rather than work, and it buries what needs acting on"`
}

func (s *Server) drift(ctx context.Context, _ *sdk.CallToolRequest, in driftInput) (*sdk.CallToolResult, any, error) {
	drifts, err := s.opts.Catalog.Drift(ctx, "", index.DriftFilter{Undeclared: in.Undeclared}, s.viewer())
	if err != nil {
		return nil, nil, err
	}
	if len(drifts) == 0 {
		return text("Nothing the catalog claims is unsupported: everything declared is observed, and every note points at something real.\n\nThis says nothing about what is running and undeclared. Pass `undeclared` for that. And if no ingester is configured there is nothing to compare against, in which case this answer means only that."), nil, nil
	}

	missing, notes, undeclared := sortDrift(drifts)

	var out strings.Builder
	out.WriteString("# Drift\n\n")

	if len(missing) > 0 {
		fmt.Fprintf(&out, "## %d declared and not observed\n\n", len(missing))
		for _, drift := range missing {
			fmt.Fprintf(&out, "- `%s` %s, declared in %s\n",
				drift.Ref, displayName(drift.Title, ""), drift.Declared)
		}
		out.WriteString("\nEach of these is watched: something observes its kind in its namespace, or its declaration names an `observed_as` that should have matched. Nothing reported them, so each one is gone, or is known to an ingester by another name, or sits behind a filter its ingester does not cross. Only the first is a declaration to remove.\n")
		out.WriteString("\nFor each, read the exact declaration with `get(ref, repository)`. Then use that proof with `declare`: replace `observed_as` when the ingester uses another ref, set `decommissioned: true` to preserve retired history, or use `remove: true, confirm: true` only when the declaration file should disappear. `drift undeclared` lists the refs in use.\n\n")
	}

	if len(notes) > 0 {
		fmt.Fprintf(&out, "## %d note ref(s) pointing at nothing\n\n", len(notes))
		for _, drift := range notes {
			fmt.Fprintf(&out, "- `%s`, written down in %s\n", drift.Ref, drift.Declared)
		}
		out.WriteString("\nThe notes are findable by search and will never appear on the thing they are about. Read each note by `id` for its proof, then replace its `refs` with `note`, or close it with status done or dropped.\n\n")
	}

	if len(undeclared) > 0 {
		fmt.Fprintf(&out, "## %d running and undeclared\n\n", len(undeclared))
		for _, drift := range undeclared {
			fmt.Fprintf(&out, "- `%s` %s, seen by %s\n",
				drift.Ref, displayName(drift.Title, ""), drift.Observed)
		}
		out.WriteString("\nThese exist and nobody has said what they are for. Declaring one is a `declare` call.\n")
	}

	return text(out.String()), nil, nil
}
