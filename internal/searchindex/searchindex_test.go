package searchindex

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"

	"github.com/solodov/recall-org-roam/internal/projection"
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

func TestUpdateFileReportsAllConflictingIDsAgainstExistingIndex(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	existingOnePath := writeIndexFile(t, filepath.Join(t.TempDir(), "existing-one.org"))
	existingTwoPath := writeIndexFile(t, filepath.Join(t.TempDir(), "existing-two.org"))
	targetPath := writeIndexFile(t, filepath.Join(t.TempDir(), "target.org"))
	if err := Rebuild(indexDir, []projection.EntryDocument{
		{ID: "shared-id", Path: existingOnePath, Headline: "Existing One", Body: "alpha"},
		{ID: "another-id", Path: existingTwoPath, Headline: "Existing Two", Body: "bravo"},
	}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	_, err := UpdateFile(indexDir, targetPath, []projection.EntryDocument{
		{ID: "shared-id", Path: targetPath, Headline: "Target One", Body: "charlie"},
		{ID: "another-id", Path: targetPath, Headline: "Target Two", Body: "delta"},
	})
	if err == nil {
		t.Fatal("expected duplicate ID error")
	}

	var duplicateErr projection.DuplicateIDsError
	if !errors.As(err, &duplicateErr) {
		t.Fatalf("expected DuplicateIDsError, got %v", err)
	}
	assertDuplicateIDs(t, duplicateErr, []projection.DuplicateID{
		{ID: "shared-id", Occurrences: []projection.DuplicateIDOccurrence{{Path: existingOnePath, Headline: "Existing One"}, {Path: targetPath, Headline: "Target One"}}},
		{ID: "another-id", Occurrences: []projection.DuplicateIDOccurrence{{Path: existingTwoPath, Headline: "Existing Two"}, {Path: targetPath, Headline: "Target Two"}}},
	})

	hits, searchErr := Search(indexDir, "alpha")
	if searchErr != nil {
		t.Fatalf("search existing content after duplicate rejection: %v", searchErr)
	}
	if len(hits) != 1 || hits[0].ID != "shared-id" {
		t.Fatalf("hits = %+v, want existing shared-id to remain indexed", hits)
	}
}

func TestRebuildStoresScheduledAndDeadlinePlanningFields(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	plannedPath := writeIndexFile(t, filepath.Join(t.TempDir(), "planned.org"))
	scheduledMinuteOfDay := 9*60 + 15
	deadlineMinuteOfDay := 17 * 60
	if err := Rebuild(indexDir, []projection.EntryDocument{{
		ID:                   "planned-id",
		Path:                 plannedPath,
		ParentID:             "parent-id",
		AncestorIDs:          []string{"root-id", "parent-id"},
		Outline:              "Root / Parent / Planned",
		Headline:             "Planned",
		Tags:                 []string{"alpha-tag", "beta-tag"},
		Style:                "habit",
		Category:             "work",
		ScheduledDate:        "2026-04-28",
		ScheduledMinuteOfDay: &scheduledMinuteOfDay,
		DeadlineDate:         "2026-04-29",
		DeadlineMinuteOfDay:  &deadlineMinuteOfDay,
		Body:                 "body",
	}, {
		ID:           "date-only-id",
		Path:         writeIndexFile(t, filepath.Join(t.TempDir(), "date-only.org")),
		Headline:     "Date Only",
		DeadlineDate: "2026-04-30",
		Body:         "body",
	}}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	index, err := openExistingIndex(indexDir)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() {
		_ = index.Close()
	}()

	plannedFields := storedFieldsForDocumentID(t, index, "planned-id", []string{"parent_id", "ancestor_id", "outline", "tag", "style", "category", "is_archived", "scheduled_date", "scheduled_minute_of_day", "deadline_date", "deadline_minute_of_day"})
	if got, want := plannedFields["parent_id"], "parent-id"; got != want {
		t.Fatalf("parent_id = %#v, want %#v", got, want)
	}
	if got, want := mustStoredStrings(t, plannedFields, "ancestor_id"), []string{"root-id", "parent-id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ancestor_id = %#v, want %#v", got, want)
	}
	if got, want := plannedFields["outline"], "Root / Parent / Planned"; got != want {
		t.Fatalf("outline = %#v, want %#v", got, want)
	}
	if got, want := mustStoredStrings(t, plannedFields, "tag"), []string{"alpha-tag", "beta-tag"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tag = %#v, want %#v", got, want)
	}
	if got, want := plannedFields["style"], "habit"; got != want {
		t.Fatalf("style = %#v, want %#v", got, want)
	}
	if got, want := plannedFields["category"], "work"; got != want {
		t.Fatalf("category = %#v, want %#v", got, want)
	}
	if got, want := plannedFields["is_archived"], false; got != want {
		t.Fatalf("is_archived = %#v, want %#v", got, want)
	}
	if got, want := plannedFields["scheduled_date"], "2026-04-28"; got != want {
		t.Fatalf("scheduled_date = %#v, want %#v", got, want)
	}
	if got, want := mustStoredFloat64(t, plannedFields, "scheduled_minute_of_day"), float64(scheduledMinuteOfDay); got != want {
		t.Fatalf("scheduled_minute_of_day = %v, want %v", got, want)
	}
	if got, want := plannedFields["deadline_date"], "2026-04-29"; got != want {
		t.Fatalf("deadline_date = %#v, want %#v", got, want)
	}
	if got, want := mustStoredFloat64(t, plannedFields, "deadline_minute_of_day"), float64(deadlineMinuteOfDay); got != want {
		t.Fatalf("deadline_minute_of_day = %v, want %v", got, want)
	}

	dateOnlyFields := storedFieldsForDocumentID(t, index, "date-only-id", []string{"deadline_date", "deadline_minute_of_day"})
	if got, want := dateOnlyFields["deadline_date"], "2026-04-30"; got != want {
		t.Fatalf("deadline_date = %#v, want %#v", got, want)
	}
	if _, ok := dateOnlyFields["deadline_minute_of_day"]; ok {
		t.Fatalf("deadline_minute_of_day = %#v, want field to be absent for date-only deadlines", dateOnlyFields["deadline_minute_of_day"])
	}
}

func TestSearchSupportsPlanningAwareDialectFilters(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	overdueScheduledYesterday := projection.EntryDocument{ID: "overdue-scheduled-yesterday", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "overdue-scheduled-yesterday.org")), Headline: "Overdue Scheduled Yesterday", ScheduledDate: "2026-04-28", Body: "alpha"}
	overdueLastWeek := projection.EntryDocument{ID: "overdue-last-week", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "overdue-last-week.org")), Headline: "Overdue Last Week", DeadlineDate: "2026-04-20", Body: "bravo"}
	overdueDeadlineEarlierTodayMinute := 9 * 60
	overdueDeadlineEarlierToday := projection.EntryDocument{ID: "overdue-deadline-earlier-today", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "overdue-deadline-earlier-today.org")), Headline: "Overdue Deadline Earlier Today", DeadlineDate: "2026-04-29", DeadlineMinuteOfDay: &overdueDeadlineEarlierTodayMinute, Body: "charlie"}
	dueTodayLaterMinute := 15 * 60
	dueTodayLater := projection.EntryDocument{ID: "due-today-later", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "due-today-later.org")), Headline: "Due Today Later", ScheduledDate: "2026-04-29", ScheduledMinuteOfDay: &dueTodayLaterMinute, Body: "delta"}
	dueThisWeek := projection.EntryDocument{ID: "due-this-week", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "due-this-week.org")), Headline: "Due This Week", DeadlineDate: "2026-05-03", Body: "echo"}
	nextWeek := projection.EntryDocument{ID: "next-week", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "next-week.org")), Headline: "Next Week", DeadlineDate: "2026-05-04", Body: "foxtrot"}
	archivedOverdue := projection.EntryDocument{ID: "archived-overdue", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "archived-overdue.org")), Headline: "Archived Overdue", IsArchived: true, DeadlineDate: "2026-04-10", Body: "golf"}
	doneOverdue := projection.EntryDocument{ID: "done-overdue", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "done-overdue.org")), Headline: "Done Overdue", Todo: "DONE", IsDone: true, DeadlineDate: "2026-04-20", Body: "hotel"}
	legacyDoneOverdue := projection.EntryDocument{ID: "legacy-done-overdue", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "legacy-done-overdue.org")), Headline: "Legacy Done Overdue", Todo: "DONE", DeadlineDate: "2026-04-18", Body: "india"}
	noPlanning := projection.EntryDocument{ID: "no-planning", Path: writeIndexFile(t, filepath.Join(t.TempDir(), "no-planning.org")), Headline: "No Planning", Body: "juliet"}
	if err := Rebuild(indexDir, []projection.EntryDocument{overdueScheduledYesterday, overdueLastWeek, overdueDeadlineEarlierToday, dueTodayLater, dueThisWeek, nextWeek, archivedOverdue, doneOverdue, legacyDoneOverdue, noPlanning}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	now := time.Date(2026, time.April, 29, 10, 30, 0, 0, time.UTC)

	hits, err := searchAt(indexDir, "is:overdue", now)
	if err != nil {
		t.Fatalf("search overdue: %v", err)
	}
	assertResultIDs(t, hits, []string{"overdue-scheduled-yesterday", "overdue-last-week", "overdue-deadline-earlier-today"})

	hits, err = searchAt(indexDir, "due:today", now)
	if err != nil {
		t.Fatalf("search due today: %v", err)
	}
	assertResultIDs(t, hits, []string{"overdue-scheduled-yesterday", "overdue-last-week", "overdue-deadline-earlier-today", "due-today-later"})

	hits, err = searchAt(indexDir, "due:this-week", now)
	if err != nil {
		t.Fatalf("search due this week: %v", err)
	}
	assertResultIDs(t, hits, []string{"overdue-scheduled-yesterday", "overdue-last-week", "overdue-deadline-earlier-today", "due-today-later", "due-this-week"})

	hits, err = searchAt(indexDir, "delta due:today", now)
	if err != nil {
		t.Fatalf("search mixed raw bleve and due today: %v", err)
	}
	assertResultIDs(t, hits, []string{"due-today-later"})
}

func TestSearchExcludesArchivedEntriesByDefaultUnlessRequested(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	visiblePath := writeIndexFile(t, filepath.Join(t.TempDir(), "visible.org"))
	archivedPath := writeIndexFile(t, filepath.Join(t.TempDir(), "archived.org"))
	if err := Rebuild(indexDir, []projection.EntryDocument{
		{ID: "visible-id", Path: visiblePath, Headline: "Visible", Body: "shared-term"},
		{ID: "archived-id", Path: archivedPath, Headline: "Archived", IsArchived: true, Body: "shared-term archived-only"},
	}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	hits, err := Search(indexDir, "shared-term")
	if err != nil {
		t.Fatalf("search default visibility: %v", err)
	}
	assertResultIDs(t, hits, []string{"visible-id"})

	hits, err = Search(indexDir, "is:archived")
	if err != nil {
		t.Fatalf("search archived dialect filter: %v", err)
	}
	assertResultIDs(t, hits, []string{"archived-id"})

	hits, err = Search(indexDir, "archived-only is:archived")
	if err != nil {
		t.Fatalf("search archived mixed query: %v", err)
	}
	assertResultIDs(t, hits, []string{"archived-id"})

	hits, err = Search(indexDir, "+is_archived:true +archived-only")
	if err != nil {
		t.Fatalf("search archived raw field query: %v", err)
	}
	assertResultIDs(t, hits, []string{"archived-id"})
}

func TestSearchPassesThroughBleveQueryStringSemantics(t *testing.T) {
	t.Helper()

	indexDir := filepath.Join(t.TempDir(), "index")
	firstPath := writeIndexFile(t, filepath.Join(t.TempDir(), "first.org"))
	secondPath := writeIndexFile(t, filepath.Join(t.TempDir(), "second.org"))
	if err := Rebuild(indexDir, []projection.EntryDocument{
		{ID: "alpha-id", Path: firstPath, ParentID: "parent-id", AncestorIDs: []string{"root-id", "parent-id"}, Outline: "Root / Unique Container / Alpha Headline", Headline: "Alpha Headline", Todo: "TODO", Tags: []string{"team", "focused"}, Style: "habit", Category: "work", Body: "alpha bravo"},
		{ID: "beta-id", Path: secondPath, ParentID: "other-parent-id", AncestorIDs: []string{"root-id", "other-parent-id"}, Outline: "Root / Other Container / Beta Headline", Headline: "Beta Headline", Todo: "DONE", Tags: []string{"team"}, Category: "home", Body: "alpha"},
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

	hits, err = Search(indexDir, "+todo:TODO +alpha")
	if err != nil {
		t.Fatalf("search todo query string: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("todo hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "+todo:DONE +bravo")
	if err != nil {
		t.Fatalf("search todo exact query string: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("todo hits = %+v, want none", hits)
	}

	hits, err = Search(indexDir, "+category:work +alpha")
	if err != nil {
		t.Fatalf("search category exact query string: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("category hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "+tag:focused +alpha")
	if err != nil {
		t.Fatalf("search tag exact query string: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("tag hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "hasprop:style")
	if err != nil {
		t.Fatalf("search hasprop query: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("hasprop hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "prop:style=habit")
	if err != nil {
		t.Fatalf("search prop query: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("prop hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "is:habit")
	if err != nil {
		t.Fatalf("search habit shortcut: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("habit hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "habit")
	if err != nil {
		t.Fatalf("search default free text without style in _all: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("default style hits = %+v, want none", hits)
	}

	hits, err = Search(indexDir, "+parent_id:parent-id +alpha")
	if err != nil {
		t.Fatalf("search parent_id exact query string: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("parent_id hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "+ancestor_id:root-id +alpha")
	if err != nil {
		t.Fatalf("search ancestor_id exact query string: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("ancestor_id hits = %+v, want both entries", hits)
	}

	hits, err = Search(indexDir, "+outline:Unique +alpha")
	if err != nil {
		t.Fatalf("search outline exact field query: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "alpha-id" {
		t.Fatalf("outline hits = %+v, want alpha-id only", hits)
	}

	hits, err = Search(indexDir, "Unique")
	if err != nil {
		t.Fatalf("search default free text without outline in _all: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("default outline hits = %+v, want none", hits)
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

func storedFieldsForDocumentID(t *testing.T, index bleve.Index, id string, fields []string) map[string]interface{} {
	t.Helper()

	query := bleve.NewTermQuery(id)
	query.SetField("id")
	results, err := collectStoredFields(index, query, fields)
	if err != nil {
		t.Fatalf("collect stored fields for %q: %v", id, err)
	}
	if len(results) != 1 {
		t.Fatalf("stored fields results = %#v, want exactly one result for %q", results, id)
	}
	return results[0]
}

func mustStoredStrings(t *testing.T, fields map[string]interface{}, key string) []string {
	t.Helper()

	rawValues, ok := fields[key].([]interface{})
	if !ok {
		t.Fatalf("stored field %q = %#v, want []interface{}", key, fields[key])
	}
	values := make([]string, 0, len(rawValues))
	for _, rawValue := range rawValues {
		value, ok := rawValue.(string)
		if !ok {
			t.Fatalf("stored field %q element = %#v, want string", key, rawValue)
		}
		values = append(values, value)
	}
	return values
}

func mustStoredFloat64(t *testing.T, fields map[string]interface{}, key string) float64 {
	t.Helper()

	value, ok := fields[key].(float64)
	if !ok {
		t.Fatalf("stored field %q = %#v, want float64", key, fields[key])
	}
	return value
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

func assertResultIDs(t *testing.T, hits []SearchResult, want []string) {
	t.Helper()

	got := make([]string, 0, len(hits))
	for _, hit := range hits {
		got = append(got, hit.ID)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("hit ids = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("hit ids = %v, want %v", got, want)
		}
	}
}

func assertDuplicateIDs(t *testing.T, got projection.DuplicateIDsError, want []projection.DuplicateID) {
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
