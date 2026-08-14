package index_test

import (
	"slices"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/index"
	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

func kindNote(id, kind string, refs ...string) *duskv1alpha1.Note {
	return &duskv1alpha1.Note{Id: id, Kind: kind, Body: "jellyfin " + kind, Refs: refs}
}

func find(kinds []vocab.Kind, namespace vocab.Namespace, name string) (vocab.Kind, bool) {
	for _, kind := range kinds {
		if kind.Namespace == namespace && kind.Name == name {
			return kind, true
		}
	}
	return vocab.Kind{}, false
}

// The vocabulary is counted out of the rows the rest of the catalog is read
// from, so it cannot disagree with them (ADR-0048).
func TestVocabularyCountsWhatIsCarried(t *testing.T) {
	db := newDB(t)
	err := db.Put(t.Context(), testRepo, mainRef, declare([]*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", ""),
		entity("service:home/navidrome", "Navidrome", ""),
		entity("host:home/nas", "The NAS", ""),
	}), nil, []*duskv1alpha1.Note{
		kindNote(".dusk/a.md", "gotcha"),
		kindNote(".dusk/b.md", "todo"),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	kinds, err := db.Vocabulary(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Vocabulary: %v", err)
	}

	for _, tt := range []struct {
		namespace vocab.Namespace
		name      string
		count     int
		role      vocab.Role
	}{
		{vocab.Entity, "service", 2, vocab.Infrastructure},
		{vocab.Entity, "host", 1, vocab.Infrastructure},
		{vocab.Note, "gotcha", 1, vocab.Warning},
		{vocab.Note, "todo", 1, vocab.Work},
		{vocab.Note, "runbook", 0, vocab.Knowledge},
	} {
		kind, ok := find(kinds, tt.namespace, tt.name)
		if !ok {
			t.Errorf("%s kind %q is not in the vocabulary", tt.namespace, tt.name)
			continue
		}
		if kind.Count != tt.count {
			t.Errorf("%s %q count = %d, want %d", tt.namespace, tt.name, kind.Count, tt.count)
		}
		if kind.Role != tt.role {
			t.Errorf("%s %q role = %q, want %q", tt.namespace, tt.name, kind.Role, tt.role)
		}
	}
}

// An alias is the operator saying two spellings are one kind, so the count
// follows the alias rather than splitting the catalog in two.
func TestVocabularyCountsAnAliasAgainstWhatItAliases(t *testing.T) {
	db := newDB(t)
	err := db.Put(t.Context(), testRepo, mainRef, declare([]*duskv1alpha1.Entity{
		entity("service:home/jellyfin", "Jellyfin", ""),
		entity("svc:home/navidrome", "Navidrome", ""),
	}), nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	err = db.PutVocabulary(t.Context(), testRepo, mainRef, []vocab.Kind{{
		Namespace: vocab.Entity, Name: "service",
		Role: vocab.Infrastructure, Aliases: []string{"svc"},
	}})
	if err != nil {
		t.Fatalf("PutVocabulary: %v", err)
	}

	kinds, err := db.Vocabulary(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Vocabulary: %v", err)
	}

	service, ok := find(kinds, vocab.Entity, "service")
	if !ok {
		t.Fatal("service is not in the vocabulary")
	}
	if service.Count != 2 {
		t.Errorf("service count = %d, want 2: the alias should count against it", service.Count)
	}
	if _, ok := find(kinds, vocab.Entity, "svc"); ok {
		t.Error("svc is in the vocabulary as its own kind, and it is an alias")
	}
}

func TestADR0049_AGotchaOutranksATodoWithoutBeingPinned(t *testing.T) {
	db := newDB(t)
	ref := "service:home/jellyfin"

	// Ids sort the wrong way round, so only the role can produce this order.
	err := db.Put(t.Context(), testRepo, mainRef,
		declare([]*duskv1alpha1.Entity{entity(ref, "Jellyfin", "")}), nil,
		[]*duskv1alpha1.Note{
			kindNote(".dusk/a-todo.md", "todo", ref),
			kindNote(".dusk/b-runbook.md", "runbook", ref),
			kindNote(".dusk/c-gotcha.md", "gotcha", ref),
		})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	notes, err := db.NotesFor(t.Context(), mainRef, ref)
	if err != nil {
		t.Fatalf("NotesFor: %v", err)
	}

	var got []string
	for _, n := range notes {
		got = append(got, n.GetKind())
	}
	if want := []string{"gotcha", "runbook", "todo"}; !slices.Equal(got, want) {
		t.Errorf("notes ranked %v, want %v", got, want)
	}
}

// Pinning still wins, because it is the deliberate exception and the role is
// the default.
func TestPinningStillOutranksTheKind(t *testing.T) {
	db := newDB(t)
	ref := "service:home/jellyfin"

	pinned := kindNote(".dusk/z-todo.md", "todo", ref)
	pinned.Pinned = true
	err := db.Put(t.Context(), testRepo, mainRef,
		declare([]*duskv1alpha1.Entity{entity(ref, "Jellyfin", "")}), nil,
		[]*duskv1alpha1.Note{kindNote(".dusk/a-gotcha.md", "gotcha", ref), pinned})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	notes, err := db.NotesFor(t.Context(), mainRef, ref)
	if err != nil {
		t.Fatalf("NotesFor: %v", err)
	}
	if len(notes) != 2 || notes[0].GetKind() != "todo" {
		t.Errorf("first note is %v, want the pinned todo", notes[0].GetKind())
	}
}

// A minted kind ranks from the moment it exists, or minting one would be a
// label and ADR-0010's warning about decorative kinds would have come true.
func TestAMintedNoteKindRanks(t *testing.T) {
	db := newDB(t)
	ref := "service:home/jellyfin"

	err := db.Put(t.Context(), testRepo, mainRef,
		declare([]*duskv1alpha1.Entity{entity(ref, "Jellyfin", "")}), nil,
		[]*duskv1alpha1.Note{
			kindNote(".dusk/a-runbook.md", "runbook", ref),
			kindNote(".dusk/b-postmortem.md", "postmortem", ref),
		})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	before, err := db.NotesFor(t.Context(), mainRef, ref)
	if err != nil {
		t.Fatalf("NotesFor: %v", err)
	}
	if before[0].GetKind() != "runbook" {
		t.Fatalf("unminted, the first note is %q, want runbook by id", before[0].GetKind())
	}

	err = db.PutVocabulary(t.Context(), testRepo, mainRef, []vocab.Kind{{
		Namespace: vocab.Note, Name: "postmortem", Role: vocab.Warning,
	}})
	if err != nil {
		t.Fatalf("PutVocabulary: %v", err)
	}

	after, err := db.NotesFor(t.Context(), mainRef, ref)
	if err != nil {
		t.Fatalf("NotesFor: %v", err)
	}
	if after[0].GetKind() != "postmortem" {
		t.Errorf("minted as a warning, the first note is %q, want postmortem", after[0].GetKind())
	}
}

// A todo whose body matches perfectly still comes after everything else, which
// is what "does not pollute those results" means operationally.
func TestSearchDemotesWorkBelowEverythingElse(t *testing.T) {
	db := newDB(t)
	err := db.Put(t.Context(), testRepo, mainRef,
		declare([]*duskv1alpha1.Entity{entity("service:home/jellyfin", "Jellyfin", "media")}), nil,
		[]*duskv1alpha1.Note{
			{Id: ".dusk/a-todo.md", Kind: "todo", Body: "jellyfin jellyfin jellyfin"},
			{Id: ".dusk/b-gotcha.md", Kind: "gotcha", Body: "jellyfin transcoding"},
		})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	results, err := db.Search(t.Context(), mainRef, "jellyfin", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("Search returned %d results, want 3: %+v", len(results), results)
	}
	if last := results[len(results)-1]; last.Ref != ".dusk/a-todo.md" {
		t.Errorf("last result is %q, want the todo", last.Ref)
	}
}

// Minting over a well-known kind re-roles it rather than sitting beside it, so
// an operator who treats todos as knowledge gets that.
func TestMintingOverAWellKnownKindReRolesIt(t *testing.T) {
	db := newDB(t)
	err := db.Put(t.Context(), testRepo, mainRef, nil, nil, []*duskv1alpha1.Note{
		{Id: ".dusk/a-todo.md", Kind: "todo", Body: "jellyfin"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	err = db.PutVocabulary(t.Context(), testRepo, mainRef, []vocab.Kind{{
		Namespace: vocab.Note, Name: "todo", Role: vocab.Knowledge,
	}})
	if err != nil {
		t.Fatalf("PutVocabulary: %v", err)
	}

	kinds, err := db.Vocabulary(t.Context(), mainRef)
	if err != nil {
		t.Fatalf("Vocabulary: %v", err)
	}

	var todos int
	for _, kind := range kinds {
		if kind.Namespace == vocab.Note && kind.Name == "todo" {
			todos++
			if kind.Role != vocab.Knowledge {
				t.Errorf("todo role = %q, want knowledge", kind.Role)
			}
		}
	}
	if todos != 1 {
		t.Errorf("todo appears %d times in the vocabulary, want 1", todos)
	}
}

// ADR-0045 wanted this and was blocked on a plugin interface. A mint is the
// operator saying it instead, and it is what stops airports being work.
func TestADR0048_MintingAKindAsReferenceQuietsItInDrift(t *testing.T) {
	db := newDB(t)
	observed := index.ObservedScope("flights")

	err := db.Put(t.Context(), observed, mainRef, declare([]*duskv1alpha1.Entity{
		entity("airport:world/bos", "Boston Logan", ""),
		entity("service:home/surprise", "Surprise", ""),
	}), nil, nil)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), observed, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	undeclared := func(t *testing.T) []string {
		t.Helper()
		drifts, err := db.Drift(t.Context(), mainRef, index.DriftFilter{Undeclared: true}, index.Unrestricted())
		if err != nil {
			t.Fatalf("Drift: %v", err)
		}
		var refs []string
		for _, drift := range drifts {
			if drift.Kind == index.DriftUndeclared {
				refs = append(refs, drift.Ref)
			}
		}
		return refs
	}

	if got := undeclared(t); !slices.Contains(got, "airport:world/bos") {
		t.Fatalf("before minting, undeclared = %v, want the airport in it", got)
	}

	err = db.PutVocabulary(t.Context(), testRepo, mainRef, []vocab.Kind{{
		Namespace: vocab.Entity, Name: "airport", Role: vocab.Reference,
	}})
	if err != nil {
		t.Fatalf("PutVocabulary: %v", err)
	}
	if err := db.SetDefaultView(t.Context(), testRepo, mainRef); err != nil {
		t.Fatalf("SetDefaultView: %v", err)
	}

	got := undeclared(t)
	if slices.Contains(got, "airport:world/bos") {
		t.Errorf("undeclared = %v, want the reference kind gone", got)
	}
	if !slices.Contains(got, "service:home/surprise") {
		t.Errorf("undeclared = %v, want infrastructure still reported", got)
	}
}
