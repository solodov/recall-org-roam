package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewServiceUsesDefaultConfigPathForRebuildAndSearch(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	notesRoot := filepath.Join(homeDir, "notes")
	writeOrgFile(t, filepath.Join(notesRoot, "alpha.org"), `* Alpha
:PROPERTIES:
:ID: alpha-id
:END:
alphabody
`)

	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(homeDir, "xdg-data"))

	configPath := filepath.Join(homeDir, "xdg-config", "org-search", "config.txtpb")
	writeConfigFile(t, configPath, "notes_root: \""+notesRoot+"\"")

	rebuildResult, err := NewService().Rebuild(context.Background(), RebuildRequest{})
	if err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	rebuildResponse, ok := rebuildResult.(RebuildResponse)
	if !ok {
		t.Fatalf("rebuildResult type = %T, want RebuildResponse", rebuildResult)
	}
	if got, want := rebuildResponse.IndexedFileCount, 1; got != want {
		t.Fatalf("indexedFileCount = %d, want %d", got, want)
	}
	if got, want := rebuildResponse.IndexedEntryCount, 1; got != want {
		t.Fatalf("indexedEntryCount = %d, want %d", got, want)
	}
	if len(rebuildResponse.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", rebuildResponse.Warnings)
	}

	searchResult, err := NewService().Search(context.Background(), SearchRequest{Query: "alphabody"})
	if err != nil {
		t.Fatalf("search index: %v", err)
	}
	searchResponse, ok := searchResult.(SearchResponse)
	if !ok {
		t.Fatalf("searchResult type = %T, want SearchResponse", searchResult)
	}
	if len(searchResponse.Hits) != 1 || searchResponse.Hits[0].ID != "alpha-id" {
		t.Fatalf("search hits = %+v, want alpha-id", searchResponse.Hits)
	}
}

func TestNewServiceUpdateFileReplacesAndDeletesOneCanonicalPath(t *testing.T) {
	t.Helper()

	rootDir := t.TempDir()
	notesRoot := filepath.Join(rootDir, "notes")
	indexDirectory := filepath.Join(rootDir, "index")
	canonicalPath := filepath.Join(notesRoot, "entry.org")
	writeOrgFile(t, canonicalPath, `* Old
:PROPERTIES:
:ID: old-id
:END:
oldbody
`)

	configPath := filepath.Join(rootDir, "config.txtpb")
	writeConfigFile(t, configPath, "notes_root: \""+notesRoot+"\"\nindex_directory: \""+indexDirectory+"\"")

	service := NewService()
	if _, err := service.Rebuild(context.Background(), RebuildRequest{ConfigPath: configPath}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	linkedPath := filepath.Join(rootDir, "linked-entry.org")
	if err := os.Symlink(canonicalPath, linkedPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	writeOrgFile(t, canonicalPath, `* New
:PROPERTIES:
:ID: new-id
:END:
newbody
`)

	updateResult, err := service.UpdateFile(context.Background(), UpdateFileRequest{ConfigPath: configPath, Path: linkedPath})
	if err != nil {
		t.Fatalf("update file: %v", err)
	}
	updateResponse, ok := updateResult.(UpdateFileResponse)
	if !ok {
		t.Fatalf("updateResult type = %T, want UpdateFileResponse", updateResult)
	}
	if got, want := updateResponse.Path, mustCanonicalPath(t, canonicalPath); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := updateResponse.DeletedEntryCount, 1; got != want {
		t.Fatalf("deletedEntryCount = %d, want %d", got, want)
	}
	if got, want := updateResponse.IndexedEntryCount, 1; got != want {
		t.Fatalf("indexedEntryCount = %d, want %d", got, want)
	}

	searchResult, err := service.Search(context.Background(), SearchRequest{ConfigPath: configPath, Query: "newbody"})
	if err != nil {
		t.Fatalf("search new content: %v", err)
	}
	searchResponse := searchResult.(SearchResponse)
	if len(searchResponse.Hits) != 1 || searchResponse.Hits[0].ID != "new-id" {
		t.Fatalf("new hits = %+v, want new-id", searchResponse.Hits)
	}

	searchResult, err = service.Search(context.Background(), SearchRequest{ConfigPath: configPath, Query: "oldbody"})
	if err != nil {
		t.Fatalf("search old content: %v", err)
	}
	if hits := searchResult.(SearchResponse).Hits; len(hits) != 0 {
		t.Fatalf("old hits = %+v, want none", hits)
	}

	if err := os.Remove(canonicalPath); err != nil {
		t.Fatalf("remove canonical path: %v", err)
	}
	updateResult, err = service.UpdateFile(context.Background(), UpdateFileRequest{ConfigPath: configPath, Path: canonicalPath})
	if err != nil {
		t.Fatalf("delete missing file from index: %v", err)
	}
	updateResponse = updateResult.(UpdateFileResponse)
	if got, want := updateResponse.DeletedEntryCount, 1; got != want {
		t.Fatalf("deletedEntryCount = %d, want %d", got, want)
	}
	if got, want := updateResponse.IndexedEntryCount, 0; got != want {
		t.Fatalf("indexedEntryCount = %d, want %d", got, want)
	}

	searchResult, err = service.Search(context.Background(), SearchRequest{ConfigPath: configPath, Query: "newbody"})
	if err != nil {
		t.Fatalf("search after deletion: %v", err)
	}
	if hits := searchResult.(SearchResponse).Hits; len(hits) != 0 {
		t.Fatalf("hits after deletion = %+v, want none", hits)
	}
}

func TestNewServiceReturnsRepairGuidanceWhenSearchingMissingIndex(t *testing.T) {
	t.Helper()

	rootDir := t.TempDir()
	notesRoot := filepath.Join(rootDir, "notes")
	configPath := filepath.Join(rootDir, "config.txtpb")
	writeConfigFile(t, configPath, "notes_root: \""+notesRoot+"\"\nindex_directory: \""+filepath.Join(rootDir, "index")+"\"")
	if err := os.MkdirAll(notesRoot, 0o755); err != nil {
		t.Fatalf("create notes root: %v", err)
	}

	_, err := NewService().Search(context.Background(), SearchRequest{ConfigPath: configPath, Query: "alpha"})
	if err == nil || !strings.Contains(err.Error(), "run rebuild") {
		t.Fatalf("expected rebuild guidance, got %v", err)
	}
}

func TestNewServiceReturnsConfigErrorsBeforeOperationErrors(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, "xdg-config"))

	_, err := NewService().Search(context.Background(), SearchRequest{})
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("expected config read error, got %v", err)
	}
}

func writeConfigFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create config directory for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file %q: %v", path, err)
	}
}

func writeOrgFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create org parent directory for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write org file %q: %v", path, err)
	}
}

func mustCanonicalPath(t *testing.T, path string) string {
	t.Helper()

	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize path %q: %v", path, err)
	}
	return canonicalPath
}
