package app

import (
	"context"
	"fmt"

	"org-search/internal/config"
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

// UpdateFileRequest stores the CLI inputs for replacing one indexed file.
type UpdateFileRequest struct {
	ConfigPath string
	Path       string
}

// SearchRequest stores the CLI inputs for one search query.
type SearchRequest struct {
	ConfigPath string
	Query      string
}

// NewService returns the default application service used by the CLI.
func NewService() Service {
	return service{}
}

type service struct{}

func (service) Rebuild(_ context.Context, request RebuildRequest) (any, error) {
	if _, err := loadConfig(request.ConfigPath); err != nil {
		return nil, err
	}
	return nil, unimplementedError{command: "rebuild"}
}

func (service) UpdateFile(_ context.Context, request UpdateFileRequest) (any, error) {
	if _, err := loadConfig(request.ConfigPath); err != nil {
		return nil, err
	}
	return nil, unimplementedError{command: "update-file"}
}

func (service) Search(_ context.Context, request SearchRequest) (any, error) {
	if _, err := loadConfig(request.ConfigPath); err != nil {
		return nil, err
	}
	return nil, unimplementedError{command: "search"}
}

func loadConfig(path string) (config.Config, error) {
	resolvedPath, err := config.ResolvePath(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	return config.Load(resolvedPath)
}

type unimplementedError struct {
	command string
}

func (err unimplementedError) Error() string {
	return fmt.Sprintf("%s is not implemented yet", err.command)
}
