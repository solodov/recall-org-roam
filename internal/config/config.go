package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"org-search/internal/gen/configpb"

	"google.golang.org/protobuf/encoding/prototext"
)

const defaultIndexDirectoryName = "index"

// Config stores the normalized org-search runtime configuration.
type Config struct {
	NotesRoot      string
	IndexDirectory string
}

// Load reads, decodes, and normalizes one txtpb config file.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	return LoadBytes(path, raw)
}

// LoadBytes decodes and normalizes one txtpb config payload.
func LoadBytes(path string, raw []byte) (Config, error) {
	var decoded configpb.Config
	if err := prototext.Unmarshal(raw, &decoded); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}

	notesRoot, err := normalizeRequiredDirectoryPath("notes_root", decoded.GetNotesRoot())
	if err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}

	indexDirectory, err := normalizeOptionalDirectoryPath(decoded.GetIndexDirectory())
	if err != nil {
		return Config{}, fmt.Errorf("validate config %q: index_directory: %w", path, err)
	}
	if indexDirectory == "" {
		indexDirectory, err = defaultIndexDirectory()
		if err != nil {
			return Config{}, fmt.Errorf("validate config %q: index_directory: %w", path, err)
		}
	}

	return Config{NotesRoot: notesRoot, IndexDirectory: indexDirectory}, nil
}

func normalizeRequiredDirectoryPath(field string, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	return normalizeDirectoryPath(trimmed)
}

func normalizeOptionalDirectoryPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	return normalizeDirectoryPath(trimmed)
}

func normalizeDirectoryPath(raw string) (string, error) {
	expanded, err := expandHomeDirectory(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("must be absolute after normalization")
	}
	return filepath.Clean(expanded), nil
}

func expandHomeDirectory(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("unsupported home-relative path %q", path)
	}
	return path, nil
}

func defaultIndexDirectory() (string, error) {
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome != "" {
		normalized, err := normalizeDirectoryPath(dataHome)
		if err != nil {
			return "", fmt.Errorf("XDG_DATA_HOME: %w", err)
		}
		return filepath.Join(normalized, "org-search", defaultIndexDirectoryName), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "org-search", defaultIndexDirectoryName), nil
}
