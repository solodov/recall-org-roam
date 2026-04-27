package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"org-search/internal/config"
	"org-search/internal/discovery"
	"org-search/internal/projection"
	"org-search/internal/searchindex"
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
}

// SearchResponse stores the JSON result for one Bleve query-string search.
type SearchResponse struct {
	Hits []SearchHit `json:"hits"`
}

// SearchHit stores the CLI search hit. Path metadata stays out of JSON and is used only for human rendering.
type SearchHit struct {
	ID       string `json:"id"`
	Path     string `json:"-"`
	FilePath string `json:"-"`
	Headline string `json:"headline"`
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

	result, err := discovery.Discover(cfg.NotesRoot, discoveryOptions(cfg))
	if err != nil {
		return nil, err
	}
	documents, err := projection.ProjectPaths(result.Paths)
	if err != nil {
		return nil, err
	}
	if err := searchindex.Rebuild(cfg.IndexDirectory, documents); err != nil {
		return nil, err
	}

	return RebuildResponse{
		IndexedFileCount:  len(result.Paths),
		IndexedEntryCount: len(documents),
		Warnings:          warningsFromDiscovery(result.Warnings),
	}, nil
}

func (service) UpdateFile(_ context.Context, request UpdateFileRequest) (any, error) {
	cfg, err := loadConfig(request.ConfigPath)
	if err != nil {
		return nil, err
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
	return SearchResponse{Hits: searchHitsFromIndex(cfg.NotesRoot, hits)}, nil
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

	documents, err := projection.ProjectFile(corpusFile.Path)
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

func searchHitsFromIndex(notesRoot string, hits []searchindex.SearchHit) []SearchHit {
	if len(hits) == 0 {
		return nil
	}
	converted := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		converted = append(converted, SearchHit{ID: hit.ID, Path: relativeSearchHitPath(notesRoot, hit.Path), FilePath: hit.Path, Headline: hit.Headline})
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
