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

	"org-search/internal/config"
	"org-search/internal/discovery"
	"org-search/internal/projection"
	"org-search/internal/searchindex"
	"org-search/internal/taghierarchy"
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
	Hits []SearchHit `json:"hits"`
}

// SearchHit stores the CLI search hit. Path metadata stays out of JSON and is used only for human rendering.
type SearchHit struct {
	ID          string   `json:"id"`
	ParentID    string   `json:"parent_id,omitempty"`
	AncestorIDs []string `json:"ancestor_id,omitempty"`
	Outline     string   `json:"outline,omitempty"`
	Path        string   `json:"-"`
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
	return SearchResponse{Hits: limitSearchHits(searchHitsFromIndex(cfg.NotesRoot, hits), request.Limit)}, nil
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

// Search handles one recall search request and maps Org hits into portable recall results.
func (provider *RecallProvider) Search(ctx context.Context, request *searchv1.SearchRequest) (*searchv1.SearchResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("search request is nil")
	}
	query := strings.TrimSpace(request.GetQuery())
	if query == "" {
		return nil, fmt.Errorf("query must be non-empty")
	}
	limit, _ := recallprovider.RequestedLimit(request)

	result, err := provider.service.Search(ctx, SearchRequest{ConfigPath: provider.configPath, Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	response, ok := result.(SearchResponse)
	if !ok {
		return nil, fmt.Errorf("search result type %T, want app.SearchResponse", result)
	}
	return &searchv1.SearchResponse{Hits: recallHitsFromSearchHits(response.Hits)}, nil
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

func searchHitsFromIndex(notesRoot string, hits []searchindex.SearchHit) []SearchHit {
	if len(hits) == 0 {
		return nil
	}
	converted := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		converted = append(converted, SearchHit{ID: hit.ID, ParentID: hit.ParentID, AncestorIDs: hit.AncestorIDs, Outline: hit.Outline, Path: relativeSearchHitPath(notesRoot, hit.Path), FilePath: hit.Path, Headline: hit.Headline})
	}
	return converted
}

func relativeSearchHitPath(notesRoot string, path string) string {
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

func limitSearchHits(hits []SearchHit, limit int) []SearchHit {
	if limit <= 0 || len(hits) <= limit {
		return hits
	}
	return hits[:limit]
}

func recallHitsFromSearchHits(hits []SearchHit) []*searchv1.SearchHit {
	if len(hits) == 0 {
		return nil
	}
	converted := make([]*searchv1.SearchHit, 0, len(hits))
	for _, hit := range hits {
		converted = append(converted, recallHitFromSearchHit(hit))
	}
	return converted
}

func recallHitFromSearchHit(hit SearchHit) *searchv1.SearchHit {
	uris := []*searchv1.NamedUri{{Name: "open", Uri: orgRoamNodeURI(hit.ID)}}
	if fileURI := fileURI(hit.FilePath); fileURI != "" {
		uris = append(uris, &searchv1.NamedUri{Name: "file", Uri: fileURI})
	}

	return &searchv1.SearchHit{
		Id:    hit.ID,
		Kind:  "org_entry",
		Title: plainRecallHeadline(hit.Headline),
		Uris:  uris,
		Group: recallGroupFromSearchHit(hit),
	}
}

func recallGroupFromSearchHit(hit SearchHit) *searchv1.SearchGroup {
	if strings.TrimSpace(hit.FilePath) == "" {
		return nil
	}
	groupTitle := strings.TrimSpace(hit.Path)
	if groupTitle == "" {
		groupTitle = filepath.Base(hit.FilePath)
	}
	return &searchv1.SearchGroup{
		Key:   "file:" + hit.FilePath,
		Title: groupTitle,
		Uris:  []*searchv1.NamedUri{{Name: "open", Uri: fileURI(hit.FilePath)}},
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

func fileURI(path string) string {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: trimmedPath}).String()
}
