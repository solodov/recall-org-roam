package searchindex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"org-search/internal/projection"
)

func TestUpdateFileReplacesOnlyTheTargetPathDocuments(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	firstPath := writeIndexFile(t, filepath.Join(t.TempDir(), "first.org"))
	secondPath := writeIndexFile(t, filepath.Join(t.TempDir(), "second.org"))

	if err := Rebuild(indexDir, []projection.EntryDocument{
		{ID: "first-id", Path: firstPath, Headline: "Old First", Body: "alpha"},
		{ID: "second-id", Path: secondPath, Headline: "Second", Body: "bravo"},
	}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	result, err := UpdateFile(indexDir, firstPath, []projection.EntryDocument{{ID: "first-new-id", Path: firstPath, Headline: "New First", Body: "charlie"}})
	if err != nil {
		t.Fatalf("update file: %v", err)
	}
	if got, want := result.DeletedEntryCount, 1; got != want {
		t.Fatalf("deletedEntryCount = %d, want %d", got, want)
	}
	if got, want := result.IndexedEntryCount, 1; got != want {
		t.Fatalf("indexedEntryCount = %d, want %d", got, want)
	}

	hits, err := Search(indexDir, "alpha")
	if err != nil {
		t.Fatalf("search old content: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("old hits = %+v, want none", hits)
	}

	hits, err = Search(indexDir, "charlie")
	if err != nil {
		t.Fatalf("search new content: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "first-new-id" {
		t.Fatalf("new hits = %+v, want first-new-id", hits)
	}

	hits, err = Search(indexDir, "bravo")
	if err != nil {
		t.Fatalf("search untouched content: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "second-id" {
		t.Fatalf("untouched hits = %+v, want second-id", hits)
	}
}

func TestUpdateFileDeletesMissingTargetFileDocuments(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	missingPath := writeIndexFile(t, filepath.Join(t.TempDir(), "missing.org"))
	if err := Rebuild(indexDir, []projection.EntryDocument{{ID: "missing-id", Path: missingPath, Headline: "Missing", Body: "vanish"}}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	if err := os.Remove(missingPath); err != nil {
		t.Fatalf("remove missing path: %v", err)
	}

	result, err := UpdateFile(indexDir, missingPath, nil)
	if err != nil {
		t.Fatalf("update missing file: %v", err)
	}
	if got, want := result.DeletedEntryCount, 1; got != want {
		t.Fatalf("deletedEntryCount = %d, want %d", got, want)
	}
	if got, want := result.IndexedEntryCount, 0; got != want {
		t.Fatalf("indexedEntryCount = %d, want %d", got, want)
	}

	hits, err := Search(indexDir, "vanish")
	if err != nil {
		t.Fatalf("search removed content: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %+v, want none", hits)
	}
}

func TestSearchCleansUpStaleFileDocuments(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	stalePath := writeIndexFile(t, filepath.Join(t.TempDir(), "stale.org"))
	if err := Rebuild(indexDir, []projection.EntryDocument{{ID: "stale-id", Path: stalePath, Headline: "Stale", Body: "stale-body"}}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	if err := os.Remove(stalePath); err != nil {
		t.Fatalf("remove stale file: %v", err)
	}

	hits, err := Search(indexDir, "stale-body")
	if err != nil {
		t.Fatalf("search stale content: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %+v, want none", hits)
	}
}

func TestSearchPassesThroughBleveQueryStringSemantics(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	firstPath := writeIndexFile(t, filepath.Join(t.TempDir(), "first.org"))
	secondPath := writeIndexFile(t, filepath.Join(t.TempDir(), "second.org"))
	if err := Rebuild(indexDir, []projection.EntryDocument{
		{ID: "alpha-id", Path: firstPath, Headline: "Alpha Headline", Body: "alpha bravo"},
		{ID: "beta-id", Path: secondPath, Headline: "Beta Headline", Body: "alpha"},
	}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	hits, err := Search(indexDir, "+alpha +bravo")
	if err != nil {
		t.Fatalf("search query string: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("hits = %+v, want alpha-id only", hits)
	}
}

func TestSearchReturnsRepairErrorWhenIndexIsMissing(t *testing.T) {
	t.Helper()

	_, err := Search(filepath.Join(t.TempDir(), "missing-index"), "alpha")
	if err == nil {
		t.Fatal("expected repair error")
	}

	var repairErr RepairError
	if !errors.As(err, &repairErr) {
		t.Fatalf("expected RepairError, got %v", err)
	}
	if !strings.Contains(err.Error(), "run rebuild") {
		t.Fatalf("error = %q, want rebuild guidance", err)
	}
}

func writeIndexFile(t *testing.T, path string) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("* entry\n"), 0o600); err != nil {
		t.Fatalf("write file %q: %v", path, err)
	}
	return path
}
