package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"org-search/internal/app"
)

type fakeService struct {
	rebuildRequest    app.RebuildRequest
	updateFileRequest app.UpdateFileRequest
	searchRequest     app.SearchRequest

	rebuildResponse    any
	updateFileResponse any
	searchResponse     any

	rebuildError    error
	updateFileError error
	searchError     error
}

func (service *fakeService) Rebuild(_ context.Context, request app.RebuildRequest) (any, error) {
	service.rebuildRequest = request
	return service.rebuildResponse, service.rebuildError
}

func (service *fakeService) UpdateFile(_ context.Context, request app.UpdateFileRequest) (any, error) {
	service.updateFileRequest = request
	return service.updateFileResponse, service.updateFileError
}

func (service *fakeService) Search(_ context.Context, request app.SearchRequest) (any, error) {
	service.searchRequest = request
	return service.searchResponse, service.searchError
}

func TestRunDispatchesRebuildCommandWithHumanOutput(t *testing.T) {
	t.Helper()

	service := &fakeService{rebuildResponse: app.RebuildResponse{
		IndexedFileCount:  2,
		IndexedEntryCount: 3,
		Warnings:          []app.Warning{{Path: "/notes/broken.org", Message: "broken symlink"}},
	}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"--config", "/tmp/config.txtpb", "rebuild"}, &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got, want := stdout.String(), "Rebuilt index\nIndexed files: 2\nIndexed entries: 3\nWarnings:\n- /notes/broken.org: broken symlink\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if got, want := service.rebuildRequest.ConfigPath, "/tmp/config.txtpb"; got != want {
		t.Fatalf("configPath = %q, want %q", got, want)
	}
}

func TestRunDispatchesRebuildCommandWithJSONOutput(t *testing.T) {
	t.Helper()

	service := &fakeService{rebuildResponse: app.RebuildResponse{IndexedFileCount: 1, IndexedEntryCount: 2}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"--json", "rebuild"}, &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got, want := stdout.String(), "{\"indexed_file_count\":1,\"indexed_entry_count\":2}\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDispatchesUpdateFileCommandWithHumanOutput(t *testing.T) {
	t.Helper()

	service := &fakeService{updateFileResponse: app.UpdateFileResponse{
		Path:              "/notes/file.org",
		DeletedEntryCount: 1,
		IndexedEntryCount: 2,
	}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"update-file", "/notes/file.org", "--config", "/tmp/config.txtpb"}, &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got, want := stdout.String(), "Updated file index\nPath: /notes/file.org\nDeleted entries: 1\nIndexed entries: 2\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := service.updateFileRequest.Path, "/notes/file.org"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := service.updateFileRequest.ConfigPath, "/tmp/config.txtpb"; got != want {
		t.Fatalf("configPath = %q, want %q", got, want)
	}
}

func TestRunDispatchesSearchCommandWithJoinedQueryAndHumanOutput(t *testing.T) {
	t.Helper()

	service := &fakeService{searchResponse: app.SearchResponse{Hits: []app.SearchHit{{ID: "alpha-id", Headline: "Alpha Headline"}, {ID: "beta-id", Headline: "Beta Headline"}}}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"search", "headline:foo", "body:bar", "--config", "/tmp/config.txtpb"}, &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got, want := service.searchRequest.Query, "headline:foo body:bar"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "2 matches\n1. alpha-id: Alpha Headline\n2. beta-id: Beta Headline\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunRendersNoMatchesHumanReadable(t *testing.T) {
	t.Helper()

	service := &fakeService{searchResponse: app.SearchResponse{}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"search", "nothing"}, &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got, want := stdout.String(), "No matches\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunRendersErrorsAsHumanReadableByDefault(t *testing.T) {
	t.Helper()

	service := &fakeService{rebuildError: errors.New("boom")}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"rebuild"}, &stdout, &stderr, service)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), "Error: boom\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRunRendersErrorsAsJSONWithFlag(t *testing.T) {
	t.Helper()

	service := &fakeService{rebuildError: errors.New("boom")}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"--json", "rebuild"}, &stdout, &stderr, service)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), "{\"error\":\"boom\"}\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
