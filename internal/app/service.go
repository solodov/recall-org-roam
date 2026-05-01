package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	searchv1 "github.com/solodov/recall/proto/recall/search/v1"
	recallprovider "github.com/solodov/recall/provider"

	"github.com/solodov/recall-org-roam/internal/config"
	"github.com/solodov/recall-org-roam/internal/discovery"
	"github.com/solodov/recall-org-roam/internal/projection"
	"github.com/solodov/recall-org-roam/internal/searchindex"
	"github.com/solodov/recall-org-roam/internal/taghierarchy"
)

// Service exposes the application operations behind the Cobra command surface.
type Service interface {
	Rebuild(context.Context, RebuildRequest) (any, error)
	UpdateFile(context.Context, UpdateFileRequest) (any, error)
	Search(context.Context, SearchRequest) (any, error)
}

// RebuildRequest stores the CLI inputs for rebuilding the full search index.
type RebuildRequest struct {
	ConfigPath string
}

// RebuildResponse stores the JSON result for a full index rebuild.
type RebuildResponse struct {
	IndexedFileCount  int       `json:"indexed_file_count"`
	IndexedEntryCount int       `json:"indexed_entry_count"`
	Warnings          []Warning `json:"warnings,omitempty"`
}

// Warning stores one operator-visible non-fatal discovery warning.
type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// UpdateFileRequest stores the CLI inputs for replacing one indexed file.
type UpdateFileRequest struct {
	ConfigPath string
	Path       string
}

// UpdateFileStatus identifies the structured update-file outcome used by editor integrations.
type UpdateFileStatus string

const (
	// UpdateFileStatusUpdated reports that one reachable corpus file was reindexed.
	UpdateFileStatusUpdated UpdateFileStatus = "updated"
	// UpdateFileStatusDeleted reports that stale indexed documents were removed for a missing file.
	UpdateFileStatusDeleted UpdateFileStatus = "deleted"
	// UpdateFileStatusSkipped reports that update-file intentionally performed no index mutation.
	UpdateFileStatusSkipped UpdateFileStatus = "skipped"
)

// UpdateFileSkipReason identifies why update-file intentionally skipped mutation.
type UpdateFileSkipReason string

const (
	// UpdateFileSkipReasonOutsideCorpus reports that the target file does not belong to the configured corpus.
	UpdateFileSkipReasonOutsideCorpus UpdateFileSkipReason = "outside_corpus"
	// UpdateFileSkipReasonNotIndexed reports that a missing file had no indexed documents to clean up.
	UpdateFileSkipReasonNotIndexed UpdateFileSkipReason = "not_indexed"
)

// UpdateFileResponse stores the structured result for one editor-safe file sync.
type UpdateFileResponse struct {
	Status            UpdateFileStatus     `json:"status"`
	Path              string               `json:"path"`
	DeletedEntryCount int                  `json:"deleted_entry_count,omitempty"`
	IndexedEntryCount int                  `json:"indexed_entry_count,omitempty"`
	SkipReason        UpdateFileSkipReason `json:"skip_reason,omitempty"`
}

// SearchRequest stores the CLI inputs for one search query.
type SearchRequest struct {
	ConfigPath string
	Query      string
	Limit      int
}

// SearchResponse stores the JSON result for one Bleve query-string search.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

// SearchResult stores one provider-local Org search result before recall protobuf mapping.
type SearchResult struct {
	ID          string   `json:"id"`
	ParentID    string   `json:"parent_id,omitempty"`
	AncestorIDs []string `json:"ancestor_ids,omitempty"`
	Outline     string   `json:"outline,omitempty"`
	Path        string   `json:"path,omitempty"`
	FilePath    string   `json:"-"`
	Headline    string   `json:"headline"`
}

// NewService returns the default application service used by the CLI.
func NewService() Service {
	return service{}
}

type service struct{}

func (service) Rebuild(_ context.Context, request RebuildRequest) (any, error) {
	cfg, err := loadConfig(request.ConfigPath)
	if err != nil {
		return nil, err
	}
	return rebuildWithConfig(cfg)
}

func (service) UpdateFile(_ context.Context, request UpdateFileRequest) (any, error) {
	cfg, err := loadConfig(request.ConfigPath)
	if err != nil {
		return nil, err
	}
	if hierarchyPathChanged, err := isTagHierarchyPath(cfg.NotesRoot, request.Path); err != nil {
		return nil, err
	} else if hierarchyPathChanged {
		return rebuildWithConfig(cfg)
	}

	prepared, err := prepareFileUpdate(cfg, request.Path)
	if err != nil {
		return nil, err
	}
	if prepared.skipReason != "" {
		return UpdateFileResponse{Status: UpdateFileStatusSkipped, Path: prepared.canonicalPath, SkipReason: prepared.skipReason}, nil
	}

	result, err := searchindex.UpdateFile(cfg.IndexDirectory, prepared.canonicalPath, prepared.documents)
	if err != nil {
		return nil, err
	}
	if prepared.missingFile {
		if result.DeletedEntryCount == 0 {
			return UpdateFileResponse{Status: UpdateFileStatusSkipped, Path: prepared.canonicalPath, SkipReason: UpdateFileSkipReasonNotIndexed}, nil
		}
		return UpdateFileResponse{Status: UpdateFileStatusDeleted, Path: prepared.canonicalPath, DeletedEntryCount: result.DeletedEntryCount}, nil
	}
	return UpdateFileResponse{
		Status:            UpdateFileStatusUpdated,
		Path:              prepared.canonicalPath,
		DeletedEntryCount: result.DeletedEntryCount,
		IndexedEntryCount: result.IndexedEntryCount,
	}, nil
}

func (service) Search(_ context.Context, request SearchRequest) (any, error) {
	cfg, err := loadConfig(request.ConfigPath)
	if err != nil {
		return nil, err
	}

	hits, err := searchindex.Search(cfg.IndexDirectory, request.Query)
	if err != nil {
		return nil, err
	}
	return SearchResponse{Results: limitSearchResults(searchResultsFromIndex(cfg.NotesRoot, hits), request.Limit)}, nil
}

// RecallProvider adapts the Org search service to recall's SearchProvider SDK.
type RecallProvider struct {
	service    Service
	configPath string
}

// NewRecallProvider returns a recall SearchProvider backed by the Org index service.
func NewRecallProvider(service Service, configPath string) *RecallProvider {
	if service == nil {
		service = NewService()
	}
	return &RecallProvider{service: service, configPath: configPath}
}

const orgEntryContentSelector = "entry:content"

// ListCapabilities advertises the Org entry selector without touching the index.
func (provider *RecallProvider) ListCapabilities(context.Context, *searchv1.ListCapabilitiesRequest) (*searchv1.ListCapabilitiesResponse, error) {
	return &searchv1.ListCapabilitiesResponse{Surfaces: []*searchv1.SearchSurface{{
		Selector:    orgEntryContentSelector,
		Title:       "Org entries",
		Description: "Search Org entry headlines, outlines, tags, and body text",
	}}}, nil
}

// Search handles one recall search request and maps Org index results into portable recall results.
func (provider *RecallProvider) Search(ctx context.Context, request *searchv1.SearchRequest) (*searchv1.SearchResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("search request is nil")
	}
	query := strings.TrimSpace(request.GetQuery())
	if query == "" {
		return nil, fmt.Errorf("query must be non-empty")
	}
	limit, _ := recallprovider.RequestedLimit(request)
	if !selectorHintsIncludeSurface(request.GetSelectorHints(), orgEntryContentSelector) {
		return &searchv1.SearchResponse{}, nil
	}

	result, err := provider.service.Search(ctx, SearchRequest{ConfigPath: provider.configPath, Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	response, ok := result.(SearchResponse)
	if !ok {
		return nil, fmt.Errorf("search result type %T, want app.SearchResponse", result)
	}
	return &searchv1.SearchResponse{Results: recallResultsFromSearchResults(response.Results)}, nil
}

func loadConfig(path string) (config.Config, error) {
	resolvedPath, err := config.ResolvePath(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	return config.Load(resolvedPath)
}

func discoveryOptions(cfg config.Config) discovery.Options {
	return discovery.Options{ExcludedDirectoryNames: cfg.ExcludedDirectoryNames}
}

func rebuildWithConfig(cfg config.Config) (RebuildResponse, error) {
	result, err := discovery.Discover(cfg.NotesRoot, discoveryOptions(cfg))
	if err != nil {
		return RebuildResponse{}, err
	}
	hierarchy, err := taghierarchy.Load(cfg.NotesRoot)
	if err != nil {
		return RebuildResponse{}, err
	}
	documents, err := projection.ProjectPaths(result.Paths, hierarchy)
	if err != nil {
		return RebuildResponse{}, err
	}
	if err := searchindex.Rebuild(cfg.IndexDirectory, documents); err != nil {
		return RebuildResponse{}, err
	}

	return RebuildResponse{
		IndexedFileCount:  len(result.Paths),
		IndexedEntryCount: len(documents),
		Warnings:          warningsFromDiscovery(result.Warnings),
	}, nil
}

func warningsFromDiscovery(warnings []discovery.Warning) []Warning {
	if len(warnings) == 0 {
		return nil
	}
	converted := make([]Warning, 0, len(warnings))
	for _, warning := range warnings {
		converted = append(converted, Warning{Path: warning.Path, Message: warning.Message})
	}
	return converted
}

type preparedFileUpdate struct {
	canonicalPath string
	documents     []projection.EntryDocument
	missingFile   bool
	skipReason    UpdateFileSkipReason
}

func prepareFileUpdate(cfg config.Config, path string) (preparedFileUpdate, error) {
	canonicalPath, err := discovery.CanonicalizePath(path)
	if err != nil {
		return preparedFileUpdate{}, fmt.Errorf("canonicalize file path %q: %w", path, err)
	}

	info, err := os.Stat(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return preparedFileUpdate{canonicalPath: canonicalPath, missingFile: true}, nil
		}
		return preparedFileUpdate{}, fmt.Errorf("stat file %q: %w", canonicalPath, err)
	}
	if info.IsDir() {
		return preparedFileUpdate{}, fmt.Errorf("file path %q is a directory", canonicalPath)
	}

	corpusFile, inCorpus, err := findCorpusFile(cfg, canonicalPath)
	if err != nil {
		return preparedFileUpdate{}, err
	}
	if !inCorpus {
		return preparedFileUpdate{canonicalPath: canonicalPath, skipReason: UpdateFileSkipReasonOutsideCorpus}, nil
	}

	hierarchy, err := taghierarchy.Load(cfg.NotesRoot)
	if err != nil {
		return preparedFileUpdate{}, err
	}
	documents, err := projection.ProjectFile(corpusFile.Path, hierarchy)
	if err != nil {
		return preparedFileUpdate{}, err
	}
	return preparedFileUpdate{canonicalPath: canonicalPath, documents: documents}, nil
}

func findCorpusFile(cfg config.Config, canonicalPath string) (discovery.File, bool, error) {
	result, err := discovery.Discover(cfg.NotesRoot, discoveryOptions(cfg))
	if err != nil {
		return discovery.File{}, false, err
	}
	for _, file := range result.Files {
		if file.CanonicalPath == canonicalPath {
			return file, true, nil
		}
	}
	return discovery.File{}, false, nil
}

func isTagHierarchyPath(notesRoot string, path string) (bool, error) {
	canonicalPath, err := discovery.CanonicalizePath(path)
	if err != nil {
		return false, fmt.Errorf("canonicalize file path %q: %w", path, err)
	}
	tagHierarchyPath, err := discovery.CanonicalizePath(taghierarchy.FilePath(notesRoot))
	if err != nil {
		return false, fmt.Errorf("canonicalize tag hierarchy path %q: %w", taghierarchy.FilePath(notesRoot), err)
	}
	return canonicalPath == tagHierarchyPath, nil
}

func searchResultsFromIndex(notesRoot string, hits []searchindex.SearchResult) []SearchResult {
	if len(hits) == 0 {
		return nil
	}
	converted := make([]SearchResult, 0, len(hits))
	for _, hit := range hits {
		converted = append(converted, SearchResult{ID: hit.ID, ParentID: hit.ParentID, AncestorIDs: hit.AncestorIDs, Outline: hit.Outline, Path: relativeSearchResultPath(notesRoot, hit.Path), FilePath: hit.Path, Headline: hit.Headline})
	}
	return converted
}

func relativeSearchResultPath(notesRoot string, path string) string {
	if path == "" {
		return ""
	}
	relativePath, err := filepath.Rel(notesRoot, path)
	if err != nil {
		return path
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(relativePath)
}

func limitSearchResults(results []SearchResult, limit int) []SearchResult {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	return results[:limit]
}

func recallResultsFromSearchResults(results []SearchResult) []*searchv1.SearchResponse_Result {
	if len(results) == 0 {
		return nil
	}
	converted := make([]*searchv1.SearchResponse_Result, 0, len(results))
	for _, result := range results {
		converted = append(converted, recallResultFromSearchResult(result))
	}
	return converted
}

func recallResultFromSearchResult(result SearchResult) *searchv1.SearchResponse_Result {
	targets := []*searchv1.OpenTarget{uriTarget(orgRoamNodeURI(result.ID))}
	if fileTarget := fileTarget(result.FilePath); fileTarget != nil {
		targets = append(targets, fileTarget)
	}

	return &searchv1.SearchResponse_Result{
		Id:       result.ID,
		Selector: orgEntryContentSelector,
		Fields:   recallFieldsFromSearchResult(result),
		Targets:  targets,
		Group:    recallGroupFromSearchResult(result),
		Format:   resultFormat([]string{"title"}, []string{"path", "outline"}),
	}
}

func recallFieldsFromSearchResult(result SearchResult) []*searchv1.SearchResponse_Result_Field {
	fields := []*searchv1.SearchResponse_Result_Field{textField("title", plainRecallHeadline(result.Headline))}
	if result.Path != "" {
		fields = append(fields, textField("path", result.Path))
	}
	if result.Outline != "" {
		fields = append(fields, textField("outline", result.Outline))
	}
	if result.ParentID != "" {
		fields = append(fields, textField("parent_id", result.ParentID))
	}
	if len(result.AncestorIDs) > 0 {
		fields = append(fields, textField("ancestor_ids", strings.Join(result.AncestorIDs, " ")))
	}
	return fields
}

func textField(key string, value string) *searchv1.SearchResponse_Result_Field {
	return &searchv1.SearchResponse_Result_Field{
		Key:   key,
		Value: &searchv1.SearchResponse_Result_Field_Text{Text: value},
	}
}

func resultFormat(titleFields []string, detailFields []string) *searchv1.SearchResponse_Result_Format {
	return &searchv1.SearchResponse_Result_Format{TitleFields: titleFields, DetailFields: detailFields}
}

func selectorHintsIncludeSurface(hints []string, surface string) bool {
	hasHint := false
	for _, hint := range hints {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			continue
		}
		hasHint = true
		if hint == surface || strings.HasPrefix(surface, hint+":") {
			return true
		}
	}
	return !hasHint
}

func recallGroupFromSearchResult(result SearchResult) *searchv1.SearchGroup {
	filePath := strings.TrimSpace(result.FilePath)
	if filePath == "" {
		return nil
	}
	groupTitle := strings.TrimSpace(result.Path)
	if groupTitle == "" {
		groupTitle = filepath.Base(filePath)
	}
	return &searchv1.SearchGroup{
		Key:     "file:" + filePath,
		Title:   groupTitle,
		Targets: []*searchv1.OpenTarget{fileTarget(filePath)},
	}
}

var (
	recallOrgBracketLinkWithDescriptionRegexp    = regexp.MustCompile(`\[\[[^\]]+\]\[([^\]]+)\]\]`)
	recallOrgBracketLinkWithoutDescriptionRegexp = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
)

func plainRecallHeadline(headline string) string {
	cleaned := strings.TrimSpace(headline)
	cleaned = recallOrgBracketLinkWithDescriptionRegexp.ReplaceAllString(cleaned, "$1")
	cleaned = recallOrgBracketLinkWithoutDescriptionRegexp.ReplaceAllString(cleaned, "$1")
	if cleaned == "" {
		return "(untitled)"
	}
	return cleaned
}

func orgRoamNodeURI(id string) string {
	return "org-protocol://roam-node?node=" + url.QueryEscape(id)
}

func uriTarget(uri string) *searchv1.OpenTarget {
	return &searchv1.OpenTarget{Target: &searchv1.OpenTarget_Uri{Uri: &searchv1.UriTarget{Uri: uri}}}
}

func fileTarget(path string) *searchv1.OpenTarget {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil
	}
	return &searchv1.OpenTarget{Target: &searchv1.OpenTarget_File{File: &searchv1.FileTarget{Path: trimmedPath}}}
}
