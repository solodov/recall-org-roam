package projection

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"org-search/internal/taghierarchy"
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

	documents, err := ProjectFile(orgPath, taghierarchy.Hierarchy{})
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
	if parent.IsDone {
		t.Fatal("parent IsDone = true, want false")
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
	if !child.IsDone {
		t.Fatal("child IsDone = false, want true")
	}
	if got, want := child.Body, "Child body."; got != want {
		t.Fatalf("child body = %q, want %q", got, want)
	}
}

func TestProjectFileUsesCustomDoneKeywords(t *testing.T) {
	t.Helper()

	orgPath := filepath.Join(t.TempDir(), "custom-todo.org")
	writeOrgFile(t, orgPath, `#+todo: TODO WAIT | DONE CANCELED
* CANCELED Finished enough
:PROPERTIES:
:ID: canceled-id
:END:
Body.
`)

	documents, err := ProjectFile(orgPath, taghierarchy.Hierarchy{})
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents = %+v, want 1 projected entry", documents)
	}
	if got, want := documents[0].Todo, "CANCELED"; got != want {
		t.Fatalf("todo = %q, want %q", got, want)
	}
	if got, want := documents[0].Headline, "Finished enough"; got != want {
		t.Fatalf("headline = %q, want %q", got, want)
	}
	if !documents[0].IsDone {
		t.Fatal("IsDone = false, want true for custom done keyword")
	}
}

func TestProjectFileInheritsCategoryFromFileKeywordAndEntryProperties(t *testing.T) {
	t.Helper()

	orgPath := filepath.Join(t.TempDir(), "category.org")
	writeOrgFile(t, orgPath, `#+CATEGORY: file-category
* Parent without ID
:PROPERTIES:
:CATEGORY: parent-category
:END:
Body.

** Child with inherited parent category
:PROPERTIES:
:ID: child-id
:END:
Body.

* Sibling with file category
:PROPERTIES:
:ID: sibling-id
:END:
Body.
`)

	documents, err := ProjectFile(orgPath, taghierarchy.Hierarchy{})
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("documents = %+v, want 2 projected entries", documents)
	}
	if got, want := documents[0].Category, "parent-category"; got != want {
		t.Fatalf("child category = %q, want %q", got, want)
	}
	if got, want := documents[1].Category, "file-category"; got != want {
		t.Fatalf("sibling category = %q, want %q", got, want)
	}
}

func TestProjectFileReadsFileCategoryFromTopPropertyDrawer(t *testing.T) {
	t.Helper()

	orgPath := filepath.Join(t.TempDir(), "file-property-category.org")
	writeOrgFile(t, orgPath, `:PROPERTIES:
:CATEGORY: file-property-category
:END:
* Entry
:PROPERTIES:
:ID: entry-id
:END:
Body.
`)

	documents, err := ProjectFile(orgPath, taghierarchy.Hierarchy{})
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents = %+v, want 1 projected entry", documents)
	}
	if got, want := documents[0].Category, "file-property-category"; got != want {
		t.Fatalf("category = %q, want %q", got, want)
	}
}

func TestProjectFileInheritsArchivedStateFromArchiveTag(t *testing.T) {
	t.Helper()

	orgPath := filepath.Join(t.TempDir(), "archived.org")
	writeOrgFile(t, orgPath, `* Archived parent :ARCHIVE:
Body.

** Archived child
:PROPERTIES:
:ID: archived-child-id
:END:
Body.

* Directly archived :ARCHIVE:
:PROPERTIES:
:ID: archived-id
:END:
Body.

* Visible entry
:PROPERTIES:
:ID: visible-id
:END:
Body.
`)

	documents, err := ProjectFile(orgPath, taghierarchy.Hierarchy{})
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 3 {
		t.Fatalf("documents = %+v, want 3 projected entries", documents)
	}
	if !documents[0].IsArchived {
		t.Fatal("archived child IsArchived = false, want true")
	}
	if !documents[1].IsArchived {
		t.Fatal("directly archived entry IsArchived = false, want true")
	}
	if documents[2].IsArchived {
		t.Fatal("visible entry IsArchived = true, want false")
	}
}

func TestProjectFileIndexesInheritedAndExpandedTags(t *testing.T) {
	t.Helper()

	hierarchy := mustTagHierarchy(t, `#+TAGS: [ topic : file_tag ]
#+TAGS: [ broad : parent_tag leaf_tag ]
#+TAGS: [ root : broad ]
`)
	orgPath := filepath.Join(t.TempDir(), "tagged.org")
	writeOrgFile(t, orgPath, `#+FILETAGS: :file_tag:
* Parent :parent_tag:
Body.

** Child :leaf_tag:
:PROPERTIES:
:ID: child-id
:END:
Body.
`)

	documents, err := ProjectFile(orgPath, hierarchy)
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents = %+v, want 1 projected entry", documents)
	}
	if got, want := documents[0].Tags, []string{"broad", "file_tag", "leaf_tag", "parent_tag", "root", "topic"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
}

func TestProjectFileIndexesScheduledAndDeadlinePlanningMetadata(t *testing.T) {
	t.Helper()

	orgPath := filepath.Join(t.TempDir(), "planning.org")
	writeOrgFile(t, orgPath, `* DONE Planned
CLOSED: [2026-04-27 Mon 18:00] SCHEDULED: <2026-04-28 Tue 09:15> DEADLINE: <2026-04-29 Wed>
:PROPERTIES:
:ID: planned-id
:END:
Body.
`)

	documents, err := ProjectFile(orgPath, taghierarchy.Hierarchy{})
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents = %+v, want 1 projected entry", documents)
	}
	if !documents[0].IsDone {
		t.Fatal("IsDone = false, want true")
	}
	if documents[0].Category != "" {
		t.Fatalf("category = %q, want empty", documents[0].Category)
	}
	if got, want := documents[0].ScheduledDate, "2026-04-28"; got != want {
		t.Fatalf("scheduledDate = %q, want %q", got, want)
	}
	if got, want := mustMinuteOfDay(t, documents[0].ScheduledMinuteOfDay), 9*60+15; got != want {
		t.Fatalf("scheduledMinuteOfDay = %d, want %d", got, want)
	}
	if got, want := documents[0].DeadlineDate, "2026-04-29"; got != want {
		t.Fatalf("deadlineDate = %q, want %q", got, want)
	}
	if documents[0].DeadlineMinuteOfDay != nil {
		t.Fatalf("deadlineMinuteOfDay = %v, want nil for date-only deadline", *documents[0].DeadlineMinuteOfDay)
	}
	if strings.Contains(documents[0].Body, ":PROPERTIES:") {
		t.Fatalf("body = %q, want property drawer metadata excluded from indexed body", documents[0].Body)
	}
}

func TestProjectFilePreservesVisiblePathMetadataForSymlinks(t *testing.T) {
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

	documents, err := ProjectFile(symlinkPath, taghierarchy.Hierarchy{})
	if err != nil {
		t.Fatalf("project file: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents = %+v, want 1 projected entry", documents)
	}
	if got, want := documents[0].Path, symlinkPath; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := documents[0].CanonicalPath, mustCanonicalPath(t, canonicalTargetPath); got != want {
		t.Fatalf("canonicalPath = %q, want %q", got, want)
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

	_, err := ProjectFile(orgPath, taghierarchy.Hierarchy{})
	if err == nil {
		t.Fatal("expected duplicate ID error")
	}

	duplicates := mustDuplicateIDsError(t, err)
	assertDuplicateIDs(t, duplicates, []DuplicateID{
		{ID: "another-id", Occurrences: []DuplicateIDOccurrence{{Path: orgPath, Headline: "Four"}, {Path: orgPath, Headline: "Three"}}},
		{ID: "shared-id", Occurrences: []DuplicateIDOccurrence{{Path: orgPath, Headline: "One"}, {Path: orgPath, Headline: "Two"}}},
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

	_, err := ProjectPaths([]string{firstPath, secondPath, thirdPath}, taghierarchy.Hierarchy{})
	if err == nil {
		t.Fatal("expected duplicate ID error")
	}

	duplicates := mustDuplicateIDsError(t, err)
	assertDuplicateIDs(t, duplicates, []DuplicateID{
		{ID: "another-id", Occurrences: []DuplicateIDOccurrence{{Path: firstPath, Headline: "Extra One"}, {Path: thirdPath, Headline: "Three"}}},
		{ID: "shared-id", Occurrences: []DuplicateIDOccurrence{{Path: firstPath, Headline: "One"}, {Path: secondPath, Headline: "Two"}}},
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

func mustTagHierarchy(t *testing.T, raw string) taghierarchy.Hierarchy {
	t.Helper()

	hierarchy, err := taghierarchy.Parse([]byte(raw), "tags.org")
	if err != nil {
		t.Fatalf("parse tag hierarchy: %v", err)
	}
	return hierarchy
}

func mustMinuteOfDay(t *testing.T, minuteOfDay *int) int {
	t.Helper()

	if minuteOfDay == nil {
		t.Fatal("expected minuteOfDay to be present")
	}
	return *minuteOfDay
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
