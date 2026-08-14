package index

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Similarity is a note that nearly says something already, and how much of its
// wording the two share.
type Similarity struct {
	Id    string
	Kind  string
	Body  string
	Score float64
}

// SimilarEnough is the share of combined wording two notes have in common
// before one is worth mentioning. A light edit keeps most of its words and two
// notes about one service share almost none, so this sits between them.
const SimilarEnough = 0.4

// candidates is how many notes the search offers for scoring. Ranked by FTS,
// so a near-duplicate is at the top of it or is not similar at all.
const candidates = 50

// SimilarNotes returns the notes whose wording overlaps body by at least
// SimilarEnough, most alike first. FTS5 finds the candidates and the overlap is
// counted here, because bm25 rank is not a threshold anybody can write.
func (db *DB) SimilarNotes(ctx context.Context, gitRef, body string, limit int) ([]Similarity, error) {
	wanted := words(body)
	if len(wanted) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	scope, scopeArgs := scopeClause("f", gitRef)
	args := append([]any{anyOf(wanted)}, scopeArgs...)
	args = append(args, candidates)

	var rows []Similarity
	err := db.gorm.WithContext(ctx).Raw(`
		SELECT n.note_id AS id, n.kind AS kind, n.body AS body
		  FROM catalog_fts f
		  JOIN notes n
		    ON n.repository = f.repository AND n.git_ref = f.git_ref AND n.note_id = f.id
		 WHERE catalog_fts MATCH ? AND f.kind_of = 'note' AND `+scope+`
		 ORDER BY rank
		 LIMIT ?`, args...,
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: notes like this at %q: %w", gitRef, err)
	}

	alike := make([]Similarity, 0, len(rows))
	for _, row := range rows {
		row.Score = overlap(wanted, words(row.Body))
		if row.Score < SimilarEnough {
			continue
		}
		alike = append(alike, row)
	}

	slices.SortFunc(alike, func(a, b Similarity) int {
		if a.Score != b.Score {
			return cmpDesc(a.Score, b.Score)
		}
		return strings.Compare(a.Id, b.Id)
	})
	return alike[:min(len(alike), limit)], nil
}

func cmpDesc(a, b float64) int {
	if a > b {
		return -1
	}
	return 1
}

// words reduces a note to what is worth comparing: lowercased, punctuation
// dropped, and nothing under three letters, which is where "the" and "is" live
// and where the accidental overlap between two short notes comes from.
func words(body string) map[string]bool {
	fields := strings.FieldsFunc(strings.ToLower(body), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	set := make(map[string]bool, len(fields))
	for _, field := range fields {
		if len(field) > 2 {
			set[field] = true
		}
	}
	return set
}

// overlap is the share of the two vocabularies that is common to both. It
// counts each word once, so repeating a word cannot make two notes look alike.
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	shared := 0
	for word := range a {
		if b[word] {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}

// termCap bounds the search expression. The longest words are the ones that
// identify a note, and a query carrying every word of a long one finds nothing
// extra for the cost.
const termCap = 32

// anyOf builds an FTS5 expression matching any of these words. Search requires
// every word, and a note that reworded a sentence is the one that dropped some.
func anyOf(set map[string]bool) string {
	words := make([]string, 0, len(set))
	for word := range set {
		words = append(words, word)
	}
	slices.SortFunc(words, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})

	terms := make([]string, 0, termCap)
	for _, word := range words[:min(len(words), termCap)] {
		terms = append(terms, `"`+word+`"`)
	}
	return strings.Join(terms, " OR ")
}
