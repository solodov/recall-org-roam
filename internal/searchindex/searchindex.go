// Package searchindex keeps Bleve isolated behind file-granular index lifecycle operations.
package searchindex

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"

	"github.com/solodov/recall-org-roam/internal/projection"
	"github.com/solodov/recall-org-roam/internal/querydialect"
)

const pageSize = 1000

// SearchResult stores the search fields the application layer needs for rendering.
type SearchResult struct {
	ID          string
	Path        string
	ParentID    string
	AncestorIDs []string
	Outline     string
	Headline    string
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

// Search runs one recall-org-roam query dialect search after removing stale file-backed documents.
// Archived entries stay hidden unless the query explicitly asks for them.
func Search(indexDirectory string, rawQuery string) ([]SearchResult, error) {
	return searchAt(indexDirectory, rawQuery, time.Now())
}

func searchAt(indexDirectory string, rawQuery string, now time.Time) ([]SearchResult, error) {
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

	compiledQuery, err := querydialect.Compile(rawQuery, now)
	if err != nil {
		return nil, err
	}
	hits, err := collectSearchResults(index, compiledQuery)
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

		indexDocument := map[string]any{
			"id":             document.ID,
			"path":           document.Path,
			"canonical_path": canonicalPath,
			"headline":       document.Headline,
			"todo":           document.Todo,
			"is_done":        document.IsDone,
			"is_archived":    document.IsArchived,
			"body":           document.Body,
		}
		if document.ParentID != "" {
			indexDocument["parent_id"] = document.ParentID
		}
		if len(document.AncestorIDs) > 0 {
			indexDocument["ancestor_id"] = document.AncestorIDs
		}
		if document.Outline != "" {
			indexDocument["outline"] = document.Outline
		}
		if len(document.Tags) > 0 {
			indexDocument["tag"] = document.Tags
		}
		if document.Style != "" {
			indexDocument["style"] = document.Style
			indexDocument["property_name"] = []string{"STYLE"}
			indexDocument["property_pair"] = []string{"STYLE=" + document.Style}
		}
		if document.Category != "" {
			indexDocument["category"] = document.Category
		}
		if document.ScheduledDate != "" {
			indexDocument["scheduled_date"] = document.ScheduledDate
		}
		if document.ScheduledMinuteOfDay != nil {
			indexDocument["scheduled_minute_of_day"] = *document.ScheduledMinuteOfDay
		}
		if document.DeadlineDate != "" {
			indexDocument["deadline_date"] = document.DeadlineDate
		}
		if document.DeadlineMinuteOfDay != nil {
			indexDocument["deadline_minute_of_day"] = *document.DeadlineMinuteOfDay
		}
		if err := batch.Index(document.ID, indexDocument); err != nil {
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

func collectSearchResults(index bleve.Index, query query.Query) ([]SearchResult, error) {
	hits := make([]SearchResult, 0)
	for from := 0; ; from += pageSize {
		request := bleve.NewSearchRequestOptions(query, pageSize, from, false)
		request.Fields = []string{"headline", "path", "parent_id", "ancestor_id", "outline"}
		result, err := index.Search(request)
		if err != nil {
			return nil, err
		}
		for _, hit := range result.Hits {
			headline, _ := hit.Fields["headline"].(string)
			path, _ := hit.Fields["path"].(string)
			parentID, _ := hit.Fields["parent_id"].(string)
			outline, _ := hit.Fields["outline"].(string)
			hits = append(hits, SearchResult{ID: hit.ID, Path: path, ParentID: parentID, AncestorIDs: storedStringSlice(hit.Fields["ancestor_id"]), Outline: outline, Headline: headline})
		}
		if len(result.Hits) < pageSize {
			return hits, nil
		}
	}
}

func storedStringSlice(value any) []string {
	switch value := value.(type) {
	case nil:
		return nil
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []string:
		return append([]string(nil), value...)
	case []interface{}:
		values := make([]string, 0, len(value))
		for _, rawValue := range value {
			stringValue, ok := rawValue.(string)
			if !ok || stringValue == "" {
				continue
			}
			values = append(values, stringValue)
		}
		if len(values) == 0 {
			return nil
		}
		return values
	default:
		return nil
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

	parentIDFieldMapping := bleve.NewKeywordFieldMapping()
	parentIDFieldMapping.Store = true
	parentIDFieldMapping.IncludeInAll = false

	ancestorIDFieldMapping := bleve.NewKeywordFieldMapping()
	ancestorIDFieldMapping.Store = true
	ancestorIDFieldMapping.IncludeInAll = false

	outlineFieldMapping := bleve.NewTextFieldMapping()
	outlineFieldMapping.Store = true
	outlineFieldMapping.IncludeInAll = false

	headlineFieldMapping := bleve.NewTextFieldMapping()
	headlineFieldMapping.Store = true

	todoFieldMapping := bleve.NewKeywordFieldMapping()
	todoFieldMapping.Store = true
	todoFieldMapping.IncludeInAll = false

	isDoneFieldMapping := bleve.NewBooleanFieldMapping()
	isDoneFieldMapping.Store = true
	isDoneFieldMapping.IncludeInAll = false

	isArchivedFieldMapping := bleve.NewBooleanFieldMapping()
	isArchivedFieldMapping.Store = true
	isArchivedFieldMapping.IncludeInAll = false

	tagFieldMapping := bleve.NewKeywordFieldMapping()
	tagFieldMapping.Store = true
	tagFieldMapping.IncludeInAll = false

	styleFieldMapping := bleve.NewKeywordFieldMapping()
	styleFieldMapping.Store = true
	styleFieldMapping.IncludeInAll = false

	propertyNameFieldMapping := bleve.NewKeywordFieldMapping()
	propertyNameFieldMapping.Store = false
	propertyNameFieldMapping.IncludeInAll = false

	propertyPairFieldMapping := bleve.NewKeywordFieldMapping()
	propertyPairFieldMapping.Store = false
	propertyPairFieldMapping.IncludeInAll = false

	categoryFieldMapping := bleve.NewKeywordFieldMapping()
	categoryFieldMapping.Store = true
	categoryFieldMapping.IncludeInAll = false

	scheduledDateFieldMapping := bleve.NewKeywordFieldMapping()
	scheduledDateFieldMapping.Store = true
	scheduledDateFieldMapping.IncludeInAll = false

	scheduledMinuteOfDayFieldMapping := bleve.NewNumericFieldMapping()
	scheduledMinuteOfDayFieldMapping.Store = true
	scheduledMinuteOfDayFieldMapping.IncludeInAll = false

	deadlineDateFieldMapping := bleve.NewKeywordFieldMapping()
	deadlineDateFieldMapping.Store = true
	deadlineDateFieldMapping.IncludeInAll = false

	deadlineMinuteOfDayFieldMapping := bleve.NewNumericFieldMapping()
	deadlineMinuteOfDayFieldMapping.Store = true
	deadlineMinuteOfDayFieldMapping.IncludeInAll = false

	bodyFieldMapping := bleve.NewTextFieldMapping()
	bodyFieldMapping.Store = false

	indexMapping.DefaultMapping.AddFieldMappingsAt("id", idFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("path", pathFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("canonical_path", canonicalPathFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("parent_id", parentIDFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("ancestor_id", ancestorIDFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("outline", outlineFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("headline", headlineFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("todo", todoFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("is_done", isDoneFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("is_archived", isArchivedFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("tag", tagFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("style", styleFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("property_name", propertyNameFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("property_pair", propertyPairFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("category", categoryFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("scheduled_date", scheduledDateFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("scheduled_minute_of_day", scheduledMinuteOfDayFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("deadline_date", deadlineDateFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("deadline_minute_of_day", deadlineMinuteOfDayFieldMapping)
	indexMapping.DefaultMapping.AddFieldMappingsAt("body", bodyFieldMapping)
	return indexMapping
}
