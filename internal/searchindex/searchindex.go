// Package searchindex keeps Bleve isolated behind file-granular index lifecycle operations.
package searchindex

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"

	"org-search/internal/projection"
)

const pageSize = 1000

// SearchHit stores the search fields the application layer needs for rendering.
type SearchHit struct {
	ID       string
	Path     string
	Headline string
}

// UpdateResult stores the exact-path replacement outcome for one file update.
type UpdateResult struct {
	DeletedEntryCount int
	IndexedEntryCount int
}

// RepairError reports that an existing index could not be opened and should be rebuilt.
type RepairError struct {
	IndexDirectory string
	Reason         string
}

func (err RepairError) Error() string {
	return fmt.Sprintf("index at %q %s; run rebuild", err.IndexDirectory, err.Reason)
}

// Rebuild recreates the full Bleve index from the provided projected documents.
func Rebuild(indexDirectory string, documents []projection.EntryDocument) error {
	if err := os.RemoveAll(indexDirectory); err != nil {
		return fmt.Errorf("reset index directory %q: %w", indexDirectory, err)
	}
	if err := os.MkdirAll(filepath.Dir(indexDirectory), 0o755); err != nil {
		return fmt.Errorf("create index parent directory for %q: %w", indexDirectory, err)
	}

	index, err := bleve.New(indexDirectory, newIndexMapping())
	if err != nil {
		return fmt.Errorf("create index %q: %w", indexDirectory, err)
	}
	defer func() {
		_ = index.Close()
	}()

	if err := indexDocuments(index, documents); err != nil {
		return err
	}
	return nil
}

// UpdateFile replaces all indexed documents for one canonical file path.
func UpdateFile(indexDirectory string, path string, documents []projection.EntryDocument) (UpdateResult, error) {
	index, err := openExistingIndex(indexDirectory)
	if err != nil {
		return UpdateResult{}, err
	}
	defer func() {
		_ = index.Close()
	}()

	if err := cleanupMissingPaths(index, path); err != nil {
		return UpdateResult{}, err
	}
	if err := rejectConflictingIDs(index, path, documents); err != nil {
		return UpdateResult{}, err
	}

	deletedEntryCount, err := deleteDocumentsByCanonicalPath(index, path)
	if err != nil {
		return UpdateResult{}, err
	}
	if err := indexDocuments(index, documents); err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{DeletedEntryCount: deletedEntryCount, IndexedEntryCount: len(documents)}, nil
}

// Search runs one Bleve query-string search after removing stale file-backed documents.
func Search(indexDirectory string, rawQuery string) ([]SearchHit, error) {
	index, err := openExistingIndex(indexDirectory)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = index.Close()
	}()

	if err := cleanupMissingPaths(index, ""); err != nil {
		return nil, err
	}

	query := bleve.NewQueryStringQuery(rawQuery)
	hits, err := collectSearchHits(index, query)
	if err != nil {
		return nil, fmt.Errorf("search index %q: %w", indexDirectory, err)
	}
	return hits, nil
}

func openExistingIndex(indexDirectory string) (bleve.Index, error) {
	if _, err := os.Stat(indexDirectory); err != nil {
		if os.IsNotExist(err) {
			return nil, RepairError{IndexDirectory: indexDirectory, Reason: "does not exist"}
		}
		return nil, fmt.Errorf("stat index directory %q: %w", indexDirectory, err)
	}

	index, err := bleve.Open(indexDirectory)
	if err != nil {
		return nil, RepairError{IndexDirectory: indexDirectory, Reason: fmt.Sprintf("could not be opened: %v", err)}
	}
	return index, nil
}

func indexDocuments(index bleve.Index, documents []projection.EntryDocument) error {
	batch := index.NewBatch()
	for _, document := range documents {
		if document.ID == "" {
			return fmt.Errorf("index document requires ID")
		}
		if document.Path == "" {
			return fmt.Errorf("index document %q requires path", document.ID)
		}
		canonicalPath := document.CanonicalPath
		if canonicalPath == "" {
			canonicalPath = document.Path
		}
		if err := batch.Index(document.ID, indexedDocument{
			ID:            document.ID,
			Path:          document.Path,
			CanonicalPath: canonicalPath,
			Headline:      document.Headline,
			Todo:          document.Todo,
			Body:          document.Body,
		}); err != nil {
			return fmt.Errorf("index document %q: %w", document.ID, err)
		}
	}
	if batch.Size() == 0 {
		return nil
	}
	if err := index.Batch(batch); err != nil {
		return fmt.Errorf("write index batch: %w", err)
	}
	return nil
}

func cleanupMissingPaths(index bleve.Index, skippedPath string) error {
	paths, err := collectIndexedCanonicalPaths(index)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if path == skippedPath {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat indexed path %q: %w", path, err)
		}
		if _, err := deleteDocumentsByCanonicalPath(index, path); err != nil {
			return err
		}
	}
	return nil
}

func rejectConflictingIDs(index bleve.Index, targetPath string, documents []projection.EntryDocument) error {
	duplicates := make([]projection.DuplicateID, 0)
	for _, document := range documents {
		occurrences, err := collectIndexedOccurrencesByID(index, document.ID)
		if err != nil {
			return fmt.Errorf("find indexed documents for duplicate ID %q: %w", document.ID, err)
		}

		conflicts := make([]projection.DuplicateIDOccurrence, 0, len(occurrences)+1)
		for _, occurrence := range occurrences {
			if occurrence.CanonicalPath == targetPath {
				continue
			}
			conflicts = append(conflicts, projection.DuplicateIDOccurrence{Path: occurrence.Path, Headline: occurrence.Headline})
		}
		if len(conflicts) == 0 {
			continue
		}
		conflicts = append(conflicts, projection.DuplicateIDOccurrence{Path: document.Path, Headline: document.Headline})
		duplicates = append(duplicates, projection.DuplicateID{ID: document.ID, Occurrences: conflicts})
	}
	if len(duplicates) > 0 {
		return projection.DuplicateIDsError{Duplicates: duplicates}
	}
	return nil
}

func collectIndexedCanonicalPaths(index bleve.Index) ([]string, error) {
	results, err := collectStoredFields(index, bleve.NewMatchAllQuery(), []string{"canonical_path", "path"})
	if err != nil {
		return nil, fmt.Errorf("list indexed paths: %w", err)
	}

	uniquePaths := make(map[string]struct{}, len(results))
	paths := make([]string, 0, len(results))
	for _, fields := range results {
		path := canonicalPathFromFields(fields)
		if path == "" {
			continue
		}
		if _, seen := uniquePaths[path]; seen {
			continue
		}
		uniquePaths[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths, nil
}

func deleteDocumentsByCanonicalPath(index bleve.Index, path string) (int, error) {
	ids, err := collectDocumentIDs(index, canonicalPathQuery(path))
	if err != nil {
		return 0, fmt.Errorf("find indexed documents for %q: %w", path, err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	batch := index.NewBatch()
	for _, id := range ids {
		batch.Delete(id)
	}
	if err := index.Batch(batch); err != nil {
		return 0, fmt.Errorf("delete indexed documents for %q: %w", path, err)
	}
	return len(ids), nil
}

type indexedOccurrence struct {
	Path          string
	CanonicalPath string
	Headline      string
}

func collectIndexedOccurrencesByID(index bleve.Index, id string) ([]indexedOccurrence, error) {
	query := bleve.NewTermQuery(id)
	query.SetField("id")
	occurrences := make([]indexedOccurrence, 0)
	for from := 0; ; from += pageSize {
		request := bleve.NewSearchRequestOptions(query, pageSize, from, false)
		request.Fields = []string{"path", "canonical_path", "headline"}
		result, err := index.Search(request)
		if err != nil {
			return nil, err
		}
		for _, hit := range result.Hits {
			path, _ := hit.Fields["path"].(string)
			headline, _ := hit.Fields["headline"].(string)
			occurrences = append(occurrences, indexedOccurrence{Path: path, CanonicalPath: canonicalPathFromFields(hit.Fields), Headline: headline})
		}
		if len(result.Hits) < pageSize {
			return occurrences, nil
		}
	}
}

func canonicalPathQuery(path string) query.Query {
	canonicalPathQuery := bleve.NewTermQuery(path)
	canonicalPathQuery.SetField("canonical_path")
	legacyPathQuery := bleve.NewTermQuery(path)
	legacyPathQuery.SetField("path")
	return bleve.NewDisjunctionQuery(canonicalPathQuery, legacyPathQuery)
}

func canonicalPathFromFields(fields map[string]interface{}) string {
	canonicalPath, _ := fields["canonical_path"].(string)
	if canonicalPath != "" {
		return canonicalPath
	}
	path, _ := fields["path"].(string)
	return path
}

func collectDocumentIDs(index bleve.Index, query query.Query) ([]string, error) {
	ids := make([]string, 0)
	for from := 0; ; from += pageSize {
		request := bleve.NewSearchRequestOptions(query, pageSize, from, false)
		result, err := index.Search(request)
		if err != nil {
			return nil, err
		}
		for _, hit := range result.Hits {
			ids = append(ids, hit.ID)
		}
		if len(result.Hits) < pageSize {
			return ids, nil
		}
	}
}

func collectSearchHits(index bleve.Index, query query.Query) ([]SearchHit, error) {
	hits := make([]SearchHit, 0)
	for from := 0; ; from += pageSize {
		request := bleve.NewSearchRequestOptions(query, pageSize, from, false)
		request.Fields = []string{"headline", "path"}
		result, err := index.Search(request)
		if err != nil {
			return nil, err
		}
		for _, hit := range result.Hits {
			headline, _ := hit.Fields["headline"].(string)
			path, _ := hit.Fields["path"].(string)
			hits = append(hits, SearchHit{ID: hit.ID, Path: path, Headline: headline})
		}
		if len(result.Hits) < pageSize {
			return hits, nil
		}
	}
}

func collectStoredFields(index bleve.Index, query query.Query, fields []string) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0)
	for from := 0; ; from += pageSize {
		request := bleve.NewSearchRequestOptions(query, pageSize, from, false)
		request.Fields = fields
		result, err := index.Search(request)
		if err != nil {
			return nil, err
		}
		for _, hit := range result.Hits {
			results = append(results, hit.Fields)
		}
		if len(result.Hits) < pageSize {
			return results, nil
		}
	}
}

func newIndexMapping() *mapping.IndexMappingImpl {
	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultMapping = bleve.NewDocumentMapping()
	indexMapping.DefaultMapping.Dynamic = false
	indexMapping.StoreDynamic = false

	idFieldMapping := bleve.NewKeywordFieldMapping()
	idFieldMapping.Store = false
	idFieldMapping.IncludeInAll = false

	pathFieldMapping := bleve.NewKeywordFieldMapping()
	pathFieldMapping.Store = true
	pathFieldMapping.IncludeInAll = false

	canonicalPathFieldMapping := bleve.NewKeywordFieldMapping()
	canonicalPathFieldMapping.Store = true
	canonicalPathFieldMapping.IncludeInAll = false

	headlineFieldMapping := bleve.NewTextFieldMapping()
	headlineFieldMapping.Store = true

	todoFieldMapping := bleve.NewKeywordFieldMapping()
	todoFieldMapping.Store = true
	todoFieldMapping.IncludeInAll = false

	bodyFieldMapping := bleve.NewTextFieldMapping()
	bodyFieldMapping.Store = false

	indexMapping.DefaultMapping.AddFieldMappingsAt("id", idFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("path", pathFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("canonical_path", canonicalPathFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("headline", headlineFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("todo", todoFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("body", bodyFieldMapping)
	return indexMapping
}

type indexedDocument struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	CanonicalPath string `json:"canonical_path"`
	Headline      string `json:"headline"`
	Todo          string `json:"todo"`
	Body          string `json:"body"`
}
