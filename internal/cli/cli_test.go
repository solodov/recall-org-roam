package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	searchv1 "github.com/solodov/recall/proto/recall/search/v1"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	"org-recall-index/internal/app"
	"org-recall-index/internal/projection"
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

func TestRunDispatchesUpdatedFileCommandWithHumanOutput(t *testing.T) {
	t.Helper()

	service := &fakeService{updateFileResponse: app.UpdateFileResponse{
		Status:            app.UpdateFileStatusUpdated,
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

func TestRunDispatchesSkippedFileCommandWithHumanOutput(t *testing.T) {
	t.Helper()

	service := &fakeService{updateFileResponse: app.UpdateFileResponse{
		Status:     app.UpdateFileStatusSkipped,
		Path:       "/notes/outside.org",
		SkipReason: app.UpdateFileSkipReasonOutsideCorpus,
	}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"update-file", "/notes/outside.org"}, &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got, want := stdout.String(), "Skipped file index update\nPath: /notes/outside.org\nReason: file is outside the configured corpus\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunDispatchesUpdateFileCommandWithJSONOutput(t *testing.T) {
	t.Helper()

	service := &fakeService{updateFileResponse: app.UpdateFileResponse{
		Status:     app.UpdateFileStatusSkipped,
		Path:       "/notes/outside.org",
		SkipReason: app.UpdateFileSkipReasonOutsideCorpus,
	}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"--json", "update-file", "/notes/outside.org"}, &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got, want := stdout.String(), "{\"status\":\"skipped\",\"path\":\"/notes/outside.org\",\"skip_reason\":\"outside_corpus\"}\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunRejectsDirectSearchCommand(t *testing.T) {
	t.Helper()

	service := &fakeService{}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"search", "alpha"}, &stdout, &stderr, service)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "unknown command") || !strings.Contains(got, "search") {
		t.Fatalf("stderr = %q, want unknown search command", got)
	}
	if service.searchRequest.Query != "" {
		t.Fatalf("search request = %+v, want no direct search dispatch", service.searchRequest)
	}
}

func TestRunServesRecallProviderWithTextprotoIO(t *testing.T) {
	t.Helper()

	service := &fakeService{searchResponse: app.SearchResponse{Hits: []app.SearchHit{{ID: "alpha-id", Path: "projects/model.org", FilePath: "/notes/projects/model.org", Headline: "Find [[https://example.invalid][Alpha]]"}}}}
	requestBytes := mustMarshalTextproto(t, &searchv1.SearchRequest{Query: "alpha", Limit: proto.Uint32(1), SelectorHints: []string{"entry"}})
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := RunWithIO(context.Background(), []string{"--config", "/tmp/config.txtpb", "recall-provider", searchv1.SearchProviderSearchPath}, strings.NewReader(string(requestBytes)), &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if got, want := service.searchRequest.ConfigPath, "/tmp/config.txtpb"; got != want {
		t.Fatalf("configPath = %q, want %q", got, want)
	}
	if got, want := service.searchRequest.Query, "alpha"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
	if got, want := service.searchRequest.Limit, 1; got != want {
		t.Fatalf("limit = %d, want %d", got, want)
	}

	var response searchv1.SearchResponse
	if err := prototext.Unmarshal([]byte(stdout.String()), &response); err != nil {
		t.Fatalf("decode response: %v; stdout = %q", err, stdout.String())
	}
	if len(response.Hits) != 1 {
		t.Fatalf("hits = %+v, want one hit", response.Hits)
	}
	hit := response.Hits[0]
	if got, want := hit.GetId(), "alpha-id"; got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}
	if got, want := hit.GetSelector(), "entry:content"; got != want {
		t.Fatalf("selector = %q, want %q", got, want)
	}
	if got, want := hit.GetTitle(), "Find Alpha"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if len(hit.GetTargets()) != 2 {
		t.Fatalf("targets = %+v, want org-roam URI and file", hit.GetTargets())
	}
	if got, want := hit.GetTargets()[0].GetUri().GetUri(), "org-protocol://roam-node?node=alpha-id"; got != want {
		t.Fatalf("primary target uri = %q, want %q", got, want)
	}
	if got, want := hit.GetTargets()[1].GetFile().GetPath(), "/notes/projects/model.org"; got != want {
		t.Fatalf("file target path = %q, want %q", got, want)
	}
	if got, want := hit.GetGroup().GetKey(), "file:/notes/projects/model.org"; got != want {
		t.Fatalf("group key = %q, want %q", got, want)
	}
	if got, want := hit.GetGroup().GetTitle(), "projects/model.org"; got != want {
		t.Fatalf("group title = %q, want %q", got, want)
	}
	if got, want := hit.GetGroup().GetTargets()[0].GetFile().GetPath(), "/notes/projects/model.org"; got != want {
		t.Fatalf("group file target path = %q, want %q", got, want)
	}
}

func TestRunServesRecallProviderCapabilitiesWithTextprotoIO(t *testing.T) {
	t.Helper()

	service := &fakeService{}
	requestBytes := mustMarshalTextproto(t, &searchv1.ListCapabilitiesRequest{})
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := RunWithIO(context.Background(), []string{"recall-provider", searchv1.SearchProviderListCapabilitiesPath}, strings.NewReader(string(requestBytes)), &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if service.searchRequest.Query != "" {
		t.Fatalf("search request = %+v, want no index search", service.searchRequest)
	}

	var response searchv1.ListCapabilitiesResponse
	if err := prototext.Unmarshal([]byte(stdout.String()), &response); err != nil {
		t.Fatalf("decode response: %v; stdout = %q", err, stdout.String())
	}
	if len(response.Surfaces) != 1 {
		t.Fatalf("surfaces = %+v, want one surface", response.Surfaces)
	}
	surface := response.Surfaces[0]
	if got, want := surface.GetSelector(), "entry:content"; got != want {
		t.Fatalf("selector = %q, want %q", got, want)
	}
	if got, want := surface.GetTitle(), "Org entries"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if got := surface.GetDescription(); !strings.Contains(got, "Org entry") {
		t.Fatalf("description = %q, want Org entry explanation", got)
	}
}

func TestRunRecallProviderSkipsUnrequestedSelectors(t *testing.T) {
	t.Helper()

	service := &fakeService{searchResponse: app.SearchResponse{Hits: []app.SearchHit{{ID: "alpha-id", Headline: "Alpha"}}}}
	requestBytes := mustMarshalTextproto(t, &searchv1.SearchRequest{Query: "alpha", SelectorHints: []string{"file"}})
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := RunWithIO(context.Background(), []string{"recall-provider", searchv1.SearchProviderSearchPath}, strings.NewReader(string(requestBytes)), &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if service.searchRequest.Query != "" {
		t.Fatalf("search request = %+v, want no index search", service.searchRequest)
	}

	var response searchv1.SearchResponse
	if err := prototext.Unmarshal([]byte(stdout.String()), &response); err != nil {
		t.Fatalf("decode response: %v; stdout = %q", err, stdout.String())
	}
	if len(response.Hits) != 0 {
		t.Fatalf("hits = %+v, want no hits for unrequested selector", response.Hits)
	}
}

func TestRunServesRecallProviderWithBinaryIO(t *testing.T) {
	t.Helper()

	service := &fakeService{searchResponse: app.SearchResponse{Hits: []app.SearchHit{{ID: "alpha-id", Headline: "Alpha"}}}}
	requestBytes, err := proto.Marshal(&searchv1.SearchRequest{Query: "alpha"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := RunWithIO(context.Background(), []string{"recall-provider", searchv1.SearchProviderSearchPath}, strings.NewReader(string(requestBytes)), &stdout, &stderr, service)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var response searchv1.SearchResponse
	if err := proto.Unmarshal([]byte(stdout.String()), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Hits) != 1 || response.Hits[0].GetId() != "alpha-id" {
		t.Fatalf("hits = %+v, want alpha-id", response.Hits)
	}
	if got, want := response.Hits[0].GetSelector(), "entry:content"; got != want {
		t.Fatalf("selector = %q, want %q", got, want)
	}
}

func TestRunRecallProviderRejectsMalformedInput(t *testing.T) {
	t.Helper()

	service := &fakeService{}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := RunWithIO(context.Background(), []string{"recall-provider", searchv1.SearchProviderSearchPath}, strings.NewReader("not protobuf"), &stdout, &stderr, service)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "decode request") {
		t.Fatalf("stderr = %q, want decode request error", got)
	}
}

func TestRunRecallProviderRendersSearchErrorsToStderr(t *testing.T) {
	t.Helper()

	service := &fakeService{searchError: errors.New("search failed")}
	requestBytes := mustMarshalTextproto(t, &searchv1.SearchRequest{Query: "alpha"})
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := RunWithIO(context.Background(), []string{"recall-provider", searchv1.SearchProviderSearchPath}, strings.NewReader(string(requestBytes)), &stdout, &stderr, service)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), "Error: search failed\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRunRendersDuplicateIDErrorsAsHumanReadableByDefault(t *testing.T) {
	t.Helper()

	service := &fakeService{rebuildError: projection.DuplicateIDsError{Duplicates: []projection.DuplicateID{{
		ID:          "another-id",
		Occurrences: []projection.DuplicateIDOccurrence{{Path: "/notes/one.org", Headline: "One"}, {Path: "/notes/two.org", Headline: "Two"}},
	}, {
		ID:          "shared-id",
		Occurrences: []projection.DuplicateIDOccurrence{{Path: "/notes/three.org", Headline: "Three"}, {Path: "/notes/four.org", Headline: "Four"}},
	}}}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"rebuild"}, &stdout, &stderr, service)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), "Error: found 2 duplicate Org IDs\n- another-id\n  - /notes/one.org: One\n  - /notes/two.org: Two\n- shared-id\n  - /notes/three.org: Three\n  - /notes/four.org: Four\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
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

func TestRunRendersDuplicateIDErrorsAsJSONWithFlag(t *testing.T) {
	t.Helper()

	service := &fakeService{rebuildError: projection.DuplicateIDsError{Duplicates: []projection.DuplicateID{{ID: "shared-id", Occurrences: []projection.DuplicateIDOccurrence{{Path: "/notes/one.org", Headline: "One"}, {Path: "/notes/two.org", Headline: "Two"}}}}}}
	var stdout strings.Builder
	var stderr strings.Builder

	exitCode := Run(context.Background(), []string{"--json", "rebuild"}, &stdout, &stderr, service)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), "{\"error\":\"found 1 duplicate org IDs: \\\"shared-id\\\" in /notes/one.org, /notes/two.org\",\"duplicates\":[{\"id\":\"shared-id\",\"occurrences\":[{\"path\":\"/notes/one.org\",\"headline\":\"One\"},{\"path\":\"/notes/two.org\",\"headline\":\"Two\"}]}]}\n"; got != want {
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

func mustMarshalTextproto(t *testing.T, message proto.Message) []byte {
	t.Helper()
	encoded, err := prototext.Marshal(message)
	if err != nil {
		t.Fatalf("marshal textproto: %v", err)
	}
	return encoded
}
