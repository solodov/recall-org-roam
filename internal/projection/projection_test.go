package projection

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectFileProjectsOnlyEntriesWithIDAndExcludesDescendantSubtrees(t *testing.T) {
	t.Helper()

	orgPath := filepath.Join(t.TempDir(), "notes.org")
	writeOrgFile(t, orgPath, `* TODO Parent
:PROPERTIES:
:ID: parent-id
:END:
Parent body.

** DONE Child
:PROPERTIES:
:ID: child-id
:END:
Child body.

* Ignored
Ignored body.
`)

	documents, err := ProjectFile(orgPath)
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("documents = %+v, want 2 projected entries", documents)
	}

	parent := documents[0]
	if got, want := parent.ID, "parent-id"; got != want {
		t.Fatalf("parent ID = %q, want %q", got, want)
	}
	if got, want := parent.Headline, "Parent"; got != want {
		t.Fatalf("parent headline = %q, want %q", got, want)
	}
	if got, want := parent.Todo, "TODO"; got != want {
		t.Fatalf("parent todo = %q, want %q", got, want)
	}
	if !strings.Contains(parent.Body, "Parent body.") {
		t.Fatalf("parent body = %q, want parent body text", parent.Body)
	}
	if strings.Contains(parent.Body, "Child body.") || strings.Contains(parent.Body, "Child") {
		t.Fatalf("parent body = %q, want no descendant subtree text", parent.Body)
	}

	child := documents[1]
	if got, want := child.ID, "child-id"; got != want {
		t.Fatalf("child ID = %q, want %q", got, want)
	}
	if got, want := child.Headline, "Child"; got != want {
		t.Fatalf("child headline = %q, want %q", got, want)
	}
	if got, want := child.Todo, "DONE"; got != want {
		t.Fatalf("child todo = %q, want %q", got, want)
	}
	if got, want := child.Body, "Child body."; got != want {
		t.Fatalf("child body = %q, want %q", got, want)
	}
}

func TestProjectFileCanonicalizesPathMetadata(t *testing.T) {
	t.Helper()

	targetDir := t.TempDir()
	canonicalTargetPath := filepath.Join(targetDir, "target.org")
	writeOrgFile(t, canonicalTargetPath, `* Target
:PROPERTIES:
:ID: target-id
:END:
Body.
`)

	rootDir := t.TempDir()
	symlinkPath := filepath.Join(rootDir, "linked.org")
	if err := os.Symlink(canonicalTargetPath, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	documents, err := ProjectFile(symlinkPath)
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents = %+v, want 1 projected entry", documents)
	}
	if got, want := documents[0].Path, mustCanonicalPath(t, canonicalTargetPath); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestProjectFileReportsAllDuplicateIDsWithinOneFile(t *testing.T) {
	t.Helper()

	orgPath := filepath.Join(t.TempDir(), "duplicates.org")
	writeOrgFile(t, orgPath, `* One
:PROPERTIES:
:ID: shared-id
:END:
First body.

* Two
:PROPERTIES:
:ID: shared-id
:END:
Second body.

* Three
:PROPERTIES:
:ID: another-id
:END:
Third body.

* Four
:PROPERTIES:
:ID: another-id
:END:
Fourth body.
`)

	_, err := ProjectFile(orgPath)
	if err == nil {
		t.Fatal("expected duplicate ID error")
	}

	duplicates := mustDuplicateIDsError(t, err)
	assertDuplicateIDs(t, duplicates, []DuplicateID{
		{ID: "another-id", Occurrences: []DuplicateIDOccurrence{{Path: mustCanonicalPath(t, orgPath), Headline: "Four"}, {Path: mustCanonicalPath(t, orgPath), Headline: "Three"}}},
		{ID: "shared-id", Occurrences: []DuplicateIDOccurrence{{Path: mustCanonicalPath(t, orgPath), Headline: "One"}, {Path: mustCanonicalPath(t, orgPath), Headline: "Two"}}},
	})
}

func TestProjectPathsReportsAllDuplicateIDsAcrossFiles(t *testing.T) {
	t.Helper()

	firstPath := filepath.Join(t.TempDir(), "first.org")
	secondPath := filepath.Join(t.TempDir(), "second.org")
	thirdPath := filepath.Join(t.TempDir(), "third.org")
	writeOrgFile(t, firstPath, `* One
:PROPERTIES:
:ID: shared-id
:END:
First body.

* Extra One
:PROPERTIES:
:ID: another-id
:END:
Body.
`)
	writeOrgFile(t, secondPath, `* Two
:PROPERTIES:
:ID: shared-id
:END:
Second body.
`)
	writeOrgFile(t, thirdPath, `* Three
:PROPERTIES:
:ID: another-id
:END:
Third body.
`)

	_, err := ProjectPaths([]string{firstPath, secondPath, thirdPath})
	if err == nil {
		t.Fatal("expected duplicate ID error")
	}

	duplicates := mustDuplicateIDsError(t, err)
	assertDuplicateIDs(t, duplicates, []DuplicateID{
		{ID: "another-id", Occurrences: []DuplicateIDOccurrence{{Path: mustCanonicalPath(t, firstPath), Headline: "Extra One"}, {Path: mustCanonicalPath(t, thirdPath), Headline: "Three"}}},
		{ID: "shared-id", Occurrences: []DuplicateIDOccurrence{{Path: mustCanonicalPath(t, firstPath), Headline: "One"}, {Path: mustCanonicalPath(t, secondPath), Headline: "Two"}}},
	})
	if !strings.Contains(err.Error(), "another-id") || !strings.Contains(err.Error(), "shared-id") {
		t.Fatalf("error = %q, want all duplicate IDs in summary", err.Error())
	}
}

func mustDuplicateIDsError(t *testing.T, err error) DuplicateIDsError {
	t.Helper()

	var duplicateErr DuplicateIDsError
	if !errors.As(err, &duplicateErr) {
		t.Fatalf("expected DuplicateIDsError, got %v", err)
	}
	return duplicateErr
}

func assertDuplicateIDs(t *testing.T, got DuplicateIDsError, want []DuplicateID) {
	t.Helper()

	if len(got.Duplicates) != len(want) {
		t.Fatalf("duplicates = %+v, want %+v", got.Duplicates, want)
	}
	for index := range want {
		if got.Duplicates[index].ID != want[index].ID {
			t.Fatalf("duplicates = %+v, want %+v", got.Duplicates, want)
		}
		if len(got.Duplicates[index].Occurrences) != len(want[index].Occurrences) {
			t.Fatalf("duplicates = %+v, want %+v", got.Duplicates, want)
		}
		for occurrenceIndex := range want[index].Occurrences {
			if got.Duplicates[index].Occurrences[occurrenceIndex] != want[index].Occurrences[occurrenceIndex] {
				t.Fatalf("duplicates = %+v, want %+v", got.Duplicates, want)
			}
		}
	}
}

func writeOrgFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory for %q: %v", path, err)
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
