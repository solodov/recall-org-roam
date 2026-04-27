package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

// UpdateFileResponse stores the JSON result for one exact-path index replacement.
type UpdateFileResponse struct {
	Path              string `json:"path"`
	DeletedEntryCount int    `json:"deleted_entry_count"`
	IndexedEntryCount int    `json:"indexed_entry_count"`
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

// SearchHit stores the minimal v1 search hit returned by the CLI.
type SearchHit struct {
	ID       string `json:"id"`
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

	result, err := discovery.Discover(cfg.NotesRoot)
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

	canonicalPath, documents, err := prepareFileUpdate(request.Path)
	if err != nil {
		return nil, err
	}
	result, err := searchindex.UpdateFile(cfg.IndexDirectory, canonicalPath, documents)
	if err != nil {
		return nil, err
	}
	return UpdateFileResponse{
		Path:              canonicalPath,
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
	return SearchResponse{Hits: searchHitsFromIndex(hits)}, nil
}

func loadConfig(path string) (config.Config, error) {
	resolvedPath, err := config.ResolvePath(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	return config.Load(resolvedPath)
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

func prepareFileUpdate(path string) (string, []projection.EntryDocument, error) {
	canonicalPath, err := discovery.CanonicalizePath(path)
	if err != nil {
		return "", nil, fmt.Errorf("canonicalize file path %q: %w", path, err)
	}

	info, err := os.Stat(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return canonicalPath, nil, nil
		}
		return "", nil, fmt.Errorf("stat file %q: %w", canonicalPath, err)
	}
	if info.IsDir() {
		return "", nil, fmt.Errorf("file path %q is a directory", canonicalPath)
	}
	if filepath.Ext(canonicalPath) != ".org" {
		return canonicalPath, nil, nil
	}

	documents, err := projection.ProjectFile(canonicalPath)
	if err != nil {
		return "", nil, err
	}
	return canonicalPath, documents, nil
}

func searchHitsFromIndex(hits []searchindex.SearchHit) []SearchHit {
	if len(hits) == 0 {
		return nil
	}
	converted := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		converted = append(converted, SearchHit{ID: hit.ID, Headline: hit.Headline})
	}
	return converted
}
