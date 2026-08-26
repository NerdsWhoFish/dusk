package index

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm/clause"

	"github.com/NerdsWhoFish/dusk/pkg/vocab"
)

// Embedder is the provider boundary semantic search needs.
type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}

// EmbeddingOptions configures the derived vector index.
type EmbeddingOptions struct {
	Embedder       Embedder
	Model          string
	RepairInterval time.Duration
	Logger         *slog.Logger
}

type semanticIndex struct {
	db       *DB
	embedder Embedder
	model    string
	repair   time.Duration
	log      *slog.Logger
	dirty    chan struct{}
	cancel   context.CancelFunc
	done     sync.WaitGroup

	cacheMu sync.Mutex
	cache   map[string][]float32
	order   []string
}

type embeddingRow struct {
	Repository  string `gorm:"primaryKey"`
	GitRef      string `gorm:"primaryKey"`
	KindOf      string `gorm:"primaryKey"`
	ID          string `gorm:"primaryKey"`
	Model       string `gorm:"primaryKey"`
	ContentHash string `gorm:"index"`
	Dimensions  int
	Vector      []byte
	UpdatedAt   time.Time
}

func (embeddingRow) TableName() string { return "embedding_rows" }

// StartEmbeddings starts an immediate backfill, incremental refreshes after
// catalog writes, and the periodic repair sweep.
func (db *DB) StartEmbeddings(ctx context.Context, opts EmbeddingOptions) error {
	if opts.Embedder == nil || strings.TrimSpace(opts.Model) == "" {
		return errors.New("index: embeddings need an embedder and model")
	}
	if opts.RepairInterval <= 0 {
		return errors.New("index: embedding repair interval must be positive")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if db.semantic != nil {
		return errors.New("index: embeddings already started")
	}
	workerCtx, cancel := context.WithCancel(ctx)
	db.semantic = &semanticIndex{
		db: db, embedder: opts.Embedder, model: opts.Model,
		repair: opts.RepairInterval, log: opts.Logger,
		dirty: make(chan struct{}, 1), cancel: cancel, cache: make(map[string][]float32),
	}
	db.signalEmbeddings()
	db.semantic.done.Add(1)
	go func() {
		defer db.semantic.done.Done()
		db.semantic.run(workerCtx)
	}()
	return nil
}

func (db *DB) signalEmbeddings() {
	if db.semantic == nil {
		return
	}
	select {
	case db.semantic.dirty <- struct{}{}:
	default:
	}
}

func (s *semanticIndex) run(ctx context.Context) {
	ticker := time.NewTicker(s.repair)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.dirty:
		}
		started := time.Now()
		updated, err := s.rebuild(ctx)
		if err != nil {
			s.log.ErrorContext(ctx, "refresh search embeddings", "error", err)
			continue
		}
		s.log.InfoContext(ctx, "search embeddings current", "updated", updated, "model", s.model, "elapsed", time.Since(started))
	}
}

type embeddingDocument struct {
	Repository, GitRef, KindOf, ID, ContentHash, Text string
}

const (
	embeddingDocumentBatch = 4
	embeddingProviderBatch = 4
	embeddingChunkBytes    = 768
)

func (s *semanticIndex) rebuild(ctx context.Context) (int, error) {
	documents, err := s.documents(ctx)
	if err != nil {
		return 0, err
	}
	existing, err := s.existing(ctx)
	if err != nil {
		return 0, err
	}
	pending := pendingDocuments(documents, existing)
	updated := 0
	for start := 0; start < len(pending); start += embeddingDocumentBatch {
		end := min(start+embeddingDocumentBatch, len(pending))
		count, err := s.embedBatch(ctx, pending[start:end])
		updated += count
		if err != nil {
			return updated, err
		}
	}
	if err := s.removeOrphans(ctx, documents, existing); err != nil {
		return updated, err
	}
	return updated, s.removeOldModels(ctx)
}

func (s *semanticIndex) existing(ctx context.Context) ([]embeddingRow, error) {
	var rows []embeddingRow
	if err := s.db.gorm.WithContext(ctx).Where("model = ?", s.model).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("index: read embeddings: %w", err)
	}
	return rows, nil
}

func pendingDocuments(documents []embeddingDocument, existing []embeddingRow) []embeddingDocument {
	current := make(map[string]embeddingRow, len(existing))
	for _, row := range existing {
		current[embeddingKey(row.Repository, row.GitRef, row.KindOf, row.ID)] = row
	}
	pending := make([]embeddingDocument, 0)
	for _, document := range documents {
		row, ok := current[embeddingKey(document.Repository, document.GitRef, document.KindOf, document.ID)]
		if !ok || row.ContentHash != document.ContentHash || len(row.Vector) == 0 {
			pending = append(pending, document)
		}
	}
	return pending
}

func (s *semanticIndex) embedBatch(ctx context.Context, batch []embeddingDocument) (int, error) {
	var input []string
	var owners []int
	for i, document := range batch {
		for _, chunk := range embeddingChunks(document.Text) {
			input = append(input, chunk)
			owners = append(owners, i)
		}
	}
	vectorsByDocument := make([][][]float32, len(batch))
	for start := 0; start < len(input); start += embeddingProviderBatch {
		end := min(start+embeddingProviderBatch, len(input))
		vectors, err := s.embedder.Embed(ctx, input[start:end])
		if err != nil {
			return 0, err
		}
		if len(vectors) != end-start {
			return 0, fmt.Errorf("index: embedding provider returned %d vectors for %d inputs", len(vectors), end-start)
		}
		for i, vector := range vectors {
			owner := owners[start+i]
			vectorsByDocument[owner] = append(vectorsByDocument[owner], vector)
		}
	}
	rows := make([]embeddingRow, len(batch))
	for i, document := range batch {
		encoded, dimensions, err := encodeVectors(vectorsByDocument[i])
		if err != nil {
			return 0, err
		}
		rows[i] = embeddingRow{
			Repository: document.Repository, GitRef: document.GitRef,
			KindOf: document.KindOf, ID: document.ID, Model: s.model,
			ContentHash: document.ContentHash, Dimensions: dimensions,
			Vector: encoded, UpdatedAt: time.Now().UTC(),
		}
	}
	if err := s.db.gorm.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&rows).Error; err != nil {
		return 0, fmt.Errorf("index: save embeddings: %w", err)
	}
	return len(rows), nil
}

func (s *semanticIndex) removeOrphans(ctx context.Context, documents []embeddingDocument, existing []embeddingRow) error {
	keys := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		keys[embeddingKey(document.Repository, document.GitRef, document.KindOf, document.ID)] = struct{}{}
	}
	for _, row := range existing {
		if _, ok := keys[embeddingKey(row.Repository, row.GitRef, row.KindOf, row.ID)]; ok {
			continue
		}
		if err := s.db.gorm.WithContext(ctx).Delete(&row).Error; err != nil {
			return fmt.Errorf("index: remove orphan embedding: %w", err)
		}
	}
	return nil
}

func (s *semanticIndex) removeOldModels(ctx context.Context) error {
	if err := s.db.gorm.WithContext(ctx).Where("model <> ?", s.model).Delete(&embeddingRow{}).Error; err != nil {
		return fmt.Errorf("index: remove old embedding models: %w", err)
	}
	return nil
}

func (s *semanticIndex) documents(ctx context.Context) ([]embeddingDocument, error) {
	var entities []entityRow
	if err := s.db.gorm.WithContext(ctx).Order("repository, git_ref, ref").Find(&entities).Error; err != nil {
		return nil, fmt.Errorf("index: list entity documents: %w", err)
	}
	var notes []noteRow
	if err := s.db.gorm.WithContext(ctx).Order("repository, git_ref, note_id").Find(&notes).Error; err != nil {
		return nil, fmt.Errorf("index: list note documents: %w", err)
	}
	var aliases []aliasRow
	if err := s.db.gorm.WithContext(ctx).Order("repository, git_ref, ref, alias").Find(&aliases).Error; err != nil {
		return nil, fmt.Errorf("index: list aliases for embeddings: %w", err)
	}
	byEntity := make(map[string][]string)
	for _, row := range aliases {
		key := embeddingKey(row.Repository, row.GitRef, "entity", row.Ref)
		byEntity[key] = append(byEntity[key], row.Alias)
	}

	documents := make([]embeddingDocument, 0, len(entities)+len(notes))
	for _, row := range entities {
		key := embeddingKey(row.Repository, row.GitRef, "entity", row.Ref)
		text := strings.Join([]string{row.Kind, row.Ref, row.Title, row.Name, row.Namespace, row.Description, string(row.Attributes), strings.Join(byEntity[key], " ")}, "\n")
		documents = append(documents, embeddingDocument{
			Repository: row.Repository, GitRef: row.GitRef, KindOf: "entity", ID: row.Ref,
			ContentHash: sourceHash(row.ContentHash, row.Version, text), Text: boundedDocument(text),
		})
	}
	for _, row := range notes {
		text := strings.Join([]string{row.Kind, row.NoteID, row.Body}, "\n")
		documents = append(documents, embeddingDocument{
			Repository: row.Repository, GitRef: row.GitRef, KindOf: "note", ID: row.NoteID,
			ContentHash: sourceHash(row.ContentHash, row.Version, text), Text: boundedDocument(text),
		})
	}
	return documents, nil
}

func sourceHash(contentHash, version, text string) string {
	if contentHash != "" {
		return contentHash
	}
	if version != "" {
		return version
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
}

func boundedDocument(text string) string {
	const maximum = 16 << 10
	text = strings.TrimSpace(text)
	if len(text) <= maximum {
		return text
	}
	text = text[:maximum]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func embeddingChunks(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	var chunks []string
	for len(text) > embeddingChunkBytes {
		cut := embeddingChunkBytes
		for !utf8.ValidString(text[:cut]) {
			cut--
		}
		if boundary := strings.LastIndexAny(text[:cut], " \n\t"); boundary >= embeddingChunkBytes/2 {
			cut = boundary
		}
		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func embeddingKey(repository, gitRef, kindOf, id string) string {
	return repository + "\x00" + gitRef + "\x00" + kindOf + "\x00" + id
}

func encodeVector(vector []float32) []byte {
	encoded, _, _ := encodeVectors([][]float32{vector})
	return encoded
}

func encodeVectors(vectors [][]float32) ([]byte, int, error) {
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, 0, errors.New("index: embedding provider returned an empty vector")
	}
	dimensions := len(vectors[0])
	out := make([]byte, len(vectors)*dimensions*4)
	for chunk, vector := range vectors {
		if len(vector) != dimensions {
			return nil, 0, errors.New("index: embedding provider changed vector dimensions")
		}
		for i, value := range vector {
			offset := (chunk*dimensions + i) * 4
			binary.LittleEndian.PutUint32(out[offset:], math.Float32bits(value))
		}
	}
	return out, dimensions, nil
}

func decodeVectors(encoded []byte, dimensions int) ([][]float32, bool) {
	width := dimensions * 4
	if dimensions <= 0 || len(encoded) == 0 || len(encoded)%width != 0 {
		return nil, false
	}
	out := make([][]float32, len(encoded)/width)
	for chunk := range out {
		out[chunk] = make([]float32, dimensions)
		for i := range out[chunk] {
			offset := chunk*width + i*4
			out[chunk][i] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[offset:]))
		}
	}
	return out, true
}

type semanticCandidate struct {
	SearchResult
	Repository, ContentHash string
	Dimensions              int
	Vector                  []byte
	Score                   float64
}

// Search fuses exact identity, FTS5, and optional semantic candidates. The
// embedding path degrades to lexical results instead of making local search
// depend on another process.
func (db *DB) Search(ctx context.Context, gitRef string, filter SearchFilter) ([]SearchResult, int, error) {
	lexical, lexicalTotal, err := db.lexicalSearch(ctx, gitRef, filter)
	if err != nil {
		return nil, 0, err
	}
	identity, err := db.identitySearch(ctx, gitRef, filter)
	if err != nil {
		return nil, 0, err
	}
	semanticEnabled := db.semantic != nil && len(strings.TrimSpace(filter.Query)) >= minSubstring
	if !semanticEnabled && identityCovered(identity, lexical) {
		return lexical, lexicalTotal, nil
	}
	minted, err := db.Minted(ctx, gitRef)
	if err != nil {
		return nil, 0, err
	}
	work := namesWithRole(minted, vocab.Note, vocab.Work)

	wide := filter
	wide.Offset = 0
	wide.Limit = max(lexicalTotal, 10_000)
	lexical, _, err = db.lexicalSearch(ctx, gitRef, wide)
	if err != nil {
		return nil, 0, err
	}
	var semantic []SearchResult
	if semanticEnabled {
		semantic, err = db.semantic.search(ctx, gitRef, filter)
		if err != nil {
			db.semantic.log.WarnContext(ctx, "semantic search unavailable", "error", err)
		}
	}
	return fuseSearch(identity, lexical, semantic, work, filter)
}

func identityCovered(identity, lexical []SearchResult) bool {
	seen := make(map[string]struct{}, len(lexical))
	for _, result := range lexical {
		seen[result.Type+"\x00"+result.Ref] = struct{}{}
	}
	for _, result := range identity {
		if _, ok := seen[result.Type+"\x00"+result.Ref]; !ok {
			return false
		}
	}
	return true
}

func (db *DB) identitySearch(ctx context.Context, gitRef string, filter SearchFilter) ([]SearchResult, error) {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query == "" {
		return nil, errors.New("index: search: query is required")
	}
	scope, args := scopeClause("e", gitRef)
	kind, kindArgs := kindClause("e", filter.Kind)
	args = append(args, kindArgs...)
	visibility, visibilityArgs := filter.Visibility.clause("e")
	if visibility != "" {
		visibility = " AND " + visibility
		args = append(args, visibilityArgs...)
	}
	predicate := `(lower(e.ref) = ? OR lower(e.name) = ? OR lower(e.title) = ?
		        OR EXISTS (SELECT 1 FROM entity_aliases a
		                    WHERE a.repository = e.repository AND a.git_ref = e.git_ref
		                      AND a.ref = e.ref AND lower(a.alias) = ?))`
	args = append(args, query, query, query, query)
	if len([]rune(query)) >= minSubstring {
		predicate = strings.TrimSuffix(predicate, ")") + `
		        OR instr(lower(e.ref), ?) > 0 OR instr(lower(e.name), ?) > 0)`
		args = append(args, query, query)
	}
	args = append(args, query, query, query)
	var rows []SearchResult
	err := db.gorm.WithContext(ctx).Raw(`
		SELECT 'entity' AS type, e.ref, e.kind, e.title, '' AS snippet, e.version,
		       'exact' AS matched_by
		  FROM entities e
		 WHERE `+scope+kind+visibility+`
		   AND `+predicate+`
		 ORDER BY CASE WHEN lower(e.ref) = ? OR lower(e.name) = ? OR lower(e.title) = ? THEN 0 ELSE 1 END,
		          e.observed, e.repository, e.ref
		 LIMIT 100`, args...).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("index: identity search %q: %w", filter.Query, err)
	}
	return rows, nil
}

func (s *semanticIndex) search(ctx context.Context, gitRef string, filter SearchFilter) ([]SearchResult, error) {
	query, err := s.queryVector(ctx, strings.TrimSpace(filter.Query))
	if err != nil {
		return nil, err
	}
	candidates, err := s.candidates(ctx, gitRef, filter)
	if err != nil {
		return nil, err
	}
	rankSemantic(candidates, query)
	return selectSemantic(candidates), nil
}

func (s *semanticIndex) candidates(ctx context.Context, gitRef string, filter SearchFilter) ([]semanticCandidate, error) {
	entities, err := s.entityCandidates(ctx, gitRef, filter)
	if err != nil {
		return nil, err
	}
	notes, err := s.noteCandidates(ctx, gitRef, filter)
	if err != nil {
		return nil, err
	}
	return append(entities, notes...), nil
}

func (s *semanticIndex) entityCandidates(ctx context.Context, gitRef string, filter SearchFilter) ([]semanticCandidate, error) {
	scope, scopeArgs := scopeClause("e", gitRef)
	kind, kindArgs := kindClause("e", filter.Kind)
	args := []any{s.model}
	args = append(args, scopeArgs...)
	args = append(args, kindArgs...)
	visibility, visibilityArgs := filter.Visibility.clause("e")
	if visibility != "" {
		visibility = " AND " + visibility
		args = append(args, visibilityArgs...)
	}
	var candidates []semanticCandidate
	err := s.db.gorm.WithContext(ctx).Raw(`
		SELECT 'entity' AS type, e.ref, e.kind, e.title, e.description AS snippet,
		       e.version, 'semantic' AS matched_by, x.repository, x.content_hash,
		       x.dimensions, x.vector
		  FROM embedding_rows x JOIN entities e
		    ON e.repository = x.repository AND e.git_ref = x.git_ref AND e.ref = x.id
		 WHERE x.model = ? AND x.kind_of = 'entity' AND `+scope+kind+visibility+`
		   AND x.content_hash = CASE WHEN e.content_hash <> '' THEN e.content_hash
		                             WHEN e.version <> '' THEN e.version ELSE x.content_hash END`, args...).Scan(&candidates).Error
	if err != nil {
		return nil, fmt.Errorf("index: semantic entity candidates: %w", err)
	}
	return candidates, nil
}

func (s *semanticIndex) noteCandidates(ctx context.Context, gitRef string, filter SearchFilter) ([]semanticCandidate, error) {
	noteScope, noteScopeArgs := scopeClause("n", gitRef)
	noteKind, noteKindArgs := kindClause("n", filter.Kind)
	noteArgs := []any{s.model}
	noteArgs = append(noteArgs, noteScopeArgs...)
	noteArgs = append(noteArgs, noteKindArgs...)
	visibility, visibilityArgs := filter.Visibility.clause("n")
	if visibility != "" {
		visibility = " AND " + visibility
		noteArgs = append(noteArgs, visibilityArgs...)
	}
	var candidates []semanticCandidate
	err := s.db.gorm.WithContext(ctx).Raw(`
		SELECT 'note' AS type, n.note_id AS ref, n.kind, '' AS title, n.body AS snippet,
		       n.content_hash AS version, 'semantic' AS matched_by, x.repository,
		       x.content_hash, x.dimensions, x.vector
		  FROM embedding_rows x JOIN notes n
		    ON n.repository = x.repository AND n.git_ref = x.git_ref AND n.note_id = x.id
		 WHERE x.model = ? AND x.kind_of = 'note' AND `+noteScope+noteKind+visibility+`
		   AND x.content_hash = CASE WHEN n.content_hash <> '' THEN n.content_hash
		                             WHEN n.version <> '' THEN n.version ELSE x.content_hash END`, noteArgs...).Scan(&candidates).Error
	if err != nil {
		return nil, fmt.Errorf("index: semantic note candidates: %w", err)
	}
	return candidates, nil
}

func rankSemantic(candidates []semanticCandidate, query []float32) {
	for i := range candidates {
		vectors, ok := decodeVectors(candidates[i].Vector, candidates[i].Dimensions)
		if !ok || len(vectors[0]) != len(query) {
			candidates[i].Score = -1
			continue
		}
		candidates[i].Score = -1
		for _, vector := range vectors {
			candidates[i].Score = max(candidates[i].Score, cosine(query, vector))
		}
	}
	slices.SortFunc(candidates, func(a, b semanticCandidate) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return strings.Compare(a.Ref, b.Ref)
	})
}

func selectSemantic(candidates []semanticCandidate) []SearchResult {
	if len(candidates) == 0 || candidates[0].Score < 0.25 {
		return nil
	}
	cutoff := candidates[0].Score - 0.12
	results := make([]SearchResult, 0, 20)
	for _, candidate := range candidates {
		if len(results) == 20 || candidate.Score < cutoff {
			break
		}
		candidate.Snippet = boundedSnippet(candidate.Snippet)
		results = append(results, candidate.SearchResult)
	}
	return results
}

func boundedSnippet(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	if len(value) <= 240 {
		return value
	}
	return value[:240] + "..."
}

func (s *semanticIndex) queryVector(ctx context.Context, query string) ([]float32, error) {
	s.cacheMu.Lock()
	if vector := s.cache[query]; vector != nil {
		copy := slices.Clone(vector)
		s.cacheMu.Unlock()
		return copy, nil
	}
	s.cacheMu.Unlock()
	vectors, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, errors.New("index: embedding provider returned no query vector")
	}
	s.cacheMu.Lock()
	s.cache[query] = slices.Clone(vectors[0])
	s.order = append(s.order, query)
	if len(s.order) > 128 {
		delete(s.cache, s.order[0])
		s.order = s.order[1:]
	}
	s.cacheMu.Unlock()
	return vectors[0], nil
}

func cosine(a, b []float32) float64 {
	var dot, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return -1
	}
	return dot / math.Sqrt(aa*bb)
}

type fusedHit struct {
	result  SearchResult
	score   float64
	source  map[string]bool
	demoted bool
}

func fuseSearch(identity, lexical, semantic []SearchResult, work []string, filter SearchFilter) ([]SearchResult, int, error) {
	hits := make(map[string]*fusedHit)
	addFused(hits, identity, "exact", 8, work)
	addFused(hits, lexical, "keyword", 1, work)
	addFused(hits, semantic, "semantic", 1, work)
	ordered := orderedFused(hits)
	return pageFused(ordered, filter), len(ordered), nil
}

func addFused(hits map[string]*fusedHit, results []SearchResult, source string, weight float64, work []string) {
	for rank, result := range results {
		key := result.Type + "\x00" + result.Ref
		hit := hits[key]
		if hit == nil {
			hit = &fusedHit{
				result: result, source: make(map[string]bool),
				demoted: result.Type == "note" && slices.Contains(work, result.Kind),
			}
			hits[key] = hit
		}
		hit.score += weight / float64(60+rank)
		hit.source[source] = true
		if hit.result.Snippet == "" && result.Snippet != "" {
			hit.result.Snippet = result.Snippet
		}
	}
}

func orderedFused(hits map[string]*fusedHit) []*fusedHit {
	ordered := make([]*fusedHit, 0, len(hits))
	for _, hit := range hits {
		hit.result.MatchedBy = matchedBy(hit.source)
		ordered = append(ordered, hit)
	}
	slices.SortFunc(ordered, compareFused)
	return ordered
}

func matchedBy(source map[string]bool) string {
	if source["semantic"] && (source["keyword"] || source["exact"]) {
		return "hybrid"
	}
	for _, name := range []string{"exact", "keyword", "semantic"} {
		if source[name] {
			return name
		}
	}
	return ""
}

func compareFused(a, b *fusedHit) int {
	if a.demoted != b.demoted {
		if a.demoted {
			return 1
		}
		return -1
	}
	if a.score > b.score {
		return -1
	}
	if a.score < b.score {
		return 1
	}
	return strings.Compare(a.result.Ref, b.result.Ref)
}

func pageFused(ordered []*fusedHit, filter SearchFilter) []SearchResult {
	start := min(max(filter.Offset, 0), len(ordered))
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	end := min(start+limit, len(ordered))
	results := make([]SearchResult, 0, end-start)
	for _, hit := range ordered[start:end] {
		results = append(results, hit.result)
	}
	return results
}
