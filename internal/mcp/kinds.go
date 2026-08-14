package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// kindsInput is one tool for both halves, like note and page. No `mint` reads
// the vocabulary; a `mint` extends it, against the token that read issued.
type kindsInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"entity or note. A filter when reading, and required when minting"`

	Mint    string   `json:"mint,omitempty" jsonschema:"a kind to add to the vocabulary. Omit to read what is there"`
	Role    string   `json:"role,omitempty" jsonschema:"what the kind is for, and required when minting. An entity kind is infrastructure or reference; a note kind is warning, knowledge or work"`
	Aliases []string `json:"aliases,omitempty" jsonschema:"other names for it, such as svc for service. Nothing can work these out, so they are worth saying"`
	Proof   string   `json:"proof,omitempty" jsonschema:"the proof token from reading the vocabulary, required to mint"`
}

func (s *Server) kinds(ctx context.Context, _ *sdk.CallToolRequest, in kindsInput) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.Mint) == "" {
		return s.readKinds(ctx, in)
	}
	return s.mintKind(ctx, in)
}

// readKinds answers what the vocabulary is, and issues the token that extends
// it. The near-match warning a mint needs is delivered by this call, which is
// why the two are one tool (ADR-0048).
func (s *Server) readKinds(ctx context.Context, in kindsInput) (*sdk.CallToolResult, any, error) {
	kinds, err := s.opts.Catalog.Vocabulary(ctx, "")
	if err != nil {
		return nil, nil, err
	}

	var out strings.Builder
	out.WriteString("# The vocabulary\n\n")
	for _, namespace := range vocab.Namespaces {
		if in.Namespace != "" && !strings.EqualFold(in.Namespace, string(namespace)) {
			continue
		}
		renderNamespace(&out, namespace, kinds)
	}

	out.WriteString("\nKinds are open: declaring one that is not here creates it, and nothing refuses that. " +
		"Minting one says what it is for, which is what makes something act on it.\n")
	return text(out.String() + s.issueVocabulary(ctx)), nil, nil
}

func renderNamespace(out *strings.Builder, namespace vocab.Namespace, kinds []vocab.Kind) {
	fmt.Fprintf(out, "## %s kinds\n\n", strings.ToUpper(string(namespace)[:1])+string(namespace)[1:])
	fmt.Fprintf(out, "%s\n\n", explain(namespace))

	shown := 0
	for _, kind := range kinds {
		if kind.Namespace != namespace {
			continue
		}
		shown++
		fmt.Fprintf(out, "- **%s** · %s · %d in the catalog", kind.Name, kind.Role, kind.Count)
		if len(kind.Aliases) > 0 {
			fmt.Fprintf(out, " · also called %s", strings.Join(kind.Aliases, ", "))
		}
		if kind.Minted {
			out.WriteString(" · minted")
		}
		out.WriteString("\n")
	}
	if shown == 0 {
		out.WriteString("Nothing carries one yet.\n")
	}
	out.WriteString("\n")
}

func explain(namespace vocab.Namespace) string {
	if namespace == vocab.Entity {
		return "`infrastructure` is something you maintain, so one running and undeclared is a gap worth reporting. " +
			"`reference` is a fact about the world, and drift stays quiet about it."
	}
	return "`warning` surfaces first on what it is attached to. " +
		"`knowledge` is how a thing works. " +
		"`work` can be closed with a status, and never outranks anything in a search."
}

// issueVocabulary mints the token a write against the vocabulary needs. The
// version is the file's hash, so a mint racing another one is refused rather
// than silently landing on top of it.
func (s *Server) issueVocabulary(ctx context.Context) string {
	if s.opts.Tokens == nil || s.opts.Writer == nil {
		return ""
	}

	minted, err := s.opts.Writer.Vocabulary(ctx)
	if err != nil {
		return fmt.Sprintf("\n---\nThe vocabulary file could not be read, so nothing can be minted right now: %s\n", err)
	}

	token := s.opts.Tokens.Issue(proof.FromKinds, map[string]string{vocab.Path: minted.Version})
	return fmt.Sprintf("\n---\nProof token `%s`. Pass it with `mint` to add a kind.\n", token.ID)
}

func (s *Server) mintKind(ctx context.Context, in kindsInput) (*sdk.CallToolResult, any, error) {
	if s.opts.Writer == nil || s.opts.Tokens == nil {
		return text("This deployment is read only, so the vocabulary can be read and not extended."), nil, nil
	}

	kind, err := parseMint(in)
	if err != nil {
		return text(fmt.Sprintf("Nothing was minted.\n\n%s", err)), nil, nil
	}

	existing, err := s.opts.Catalog.Vocabulary(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	if clash, ok := vocab.Lookup(kind.Namespace, kind.Name, existing); ok {
		return text(refuseMint(kind, clash)), nil, nil
	}

	result, err := s.opts.Writer.MintKind(ctx, in.Proof, kind)
	if err != nil {
		return text(fmt.Sprintf("Nothing was minted.\n\n%s", err)), nil, nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Minted the %s kind `%s`, for %s.\n\nCommit: %s\n\n%s\n",
		kind.Namespace, kind.Name, kind.Role, result.URL, effect(kind))
	if near := vocab.Nearest(kind.Namespace, kind.Name, existing); len(near) > 0 {
		fmt.Fprintf(&out, "\nIt is close to %s, which already exists. If they are the same thing, "+
			"mint an alias on that one instead of keeping both.\n", quoteNames(near))
	}
	out.WriteString("\nIt reaches the catalog on the next reconcile, which the push already triggered.\n")
	return text(out.String()), nil, nil
}

// refuseMint is the one case a mint is refused. Adding a second row meaning
// the same thing is not extending a vocabulary, so the answer names the alias
// that would say it properly.
func refuseMint(wanted, clash vocab.Kind) string {
	if wanted.Name == clash.Name {
		return fmt.Sprintf("The %s kind `%s` already exists, for %s. Nothing was minted.",
			clash.Namespace, clash.Name, clash.Role)
	}
	return fmt.Sprintf("`%s` is another spelling of `%s`, which the %s vocabulary already has. Nothing was minted.\n\n"+
		"If they are the same thing, mint `%s` with `%s` in its aliases. "+
		"If they are genuinely different, pick a name that is not a spelling of one that exists.",
		wanted.Name, clash.Name, clash.Namespace, clash.Name, wanted.Name)
}

// effect says what the role just changed, because a mint that changed nothing
// would be a label and would be chosen carelessly.
func effect(kind vocab.Kind) string {
	switch kind.Role {
	case vocab.Reference:
		return "Drift will stop listing these as running and undeclared."
	case vocab.Infrastructure:
		return "Drift will list one of these running and undeclared as a gap."
	case vocab.Warning:
		return "These now surface first on what they are attached to."
	case vocab.Work:
		return "These can be closed with a status, and will never outrank anything in a search."
	default:
		return "These rank as knowledge: above work, below a warning."
	}
}

func parseMint(in kindsInput) (vocab.Kind, error) {
	namespace, err := vocab.ValidNamespace(in.Namespace)
	if err != nil {
		return vocab.Kind{}, err
	}
	name, err := vocab.ValidName(in.Mint)
	if err != nil {
		return vocab.Kind{}, err
	}
	role, err := vocab.ValidRole(namespace, in.Role)
	if err != nil {
		return vocab.Kind{}, err
	}

	aliases := make([]string, 0, len(in.Aliases))
	for _, alias := range in.Aliases {
		valid, err := vocab.ValidName(alias)
		if err != nil {
			return vocab.Kind{}, err
		}
		aliases = append(aliases, valid)
	}
	return vocab.Kind{Namespace: namespace, Name: name, Role: role, Aliases: aliases, Minted: true}, nil
}

// warnAboutKind is what stops the vocabulary fragmenting through the tool most
// kinds actually arrive by. The write has already happened: refusing it would
// delete ADR-0007's open vocabulary, so the answer says what it nearly matched.
func (s *Server) warnAboutKind(ctx context.Context, namespace vocab.Namespace, name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}

	kinds, err := s.opts.Catalog.Vocabulary(ctx, "")
	if err != nil {
		return ""
	}

	if known, ok := vocab.Lookup(namespace, name, kinds); ok {
		if known.Name == name {
			return ""
		}
		return fmt.Sprintf("\n\n`%s` is an alias of `%s`. The catalog will carry it as written; "+
			"use `%s` if you want them to read as one kind.", name, known.Name, known.Name)
	}

	near := vocab.Nearest(namespace, name, kinds)
	if len(near) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nThat created the %s kind `%s`, and the catalog already has %s. "+
		"If they are the same thing, that is a split worth closing with `kinds`.",
		namespace, name, quoteNames(near))
}

func quoteNames(kinds []vocab.Kind) string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, "`"+kind.Name+"`")
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
