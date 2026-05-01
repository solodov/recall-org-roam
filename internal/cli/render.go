package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/solodov/recall-org-roam/internal/app"
	"github.com/solodov/recall-org-roam/internal/projection"
)

func writeResult(writer io.Writer, value any, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(writer, value)
	}

	switch value := value.(type) {
	case app.RebuildResponse:
		return writeHumanRebuild(writer, value)
	case app.UpdateFileResponse:
		return writeHumanUpdateFile(writer, value)
	default:
		return writeJSON(writer, value)
	}
}

func writeHumanRebuild(writer io.Writer, response app.RebuildResponse) error {
	if _, err := fmt.Fprintf(writer, "Rebuilt index\nIndexed files: %d\nIndexed entries: %d\n", response.IndexedFileCount, response.IndexedEntryCount); err != nil {
		return err
	}
	if len(response.Warnings) == 0 {
		return nil
	}
	if _, err := io.WriteString(writer, "Warnings:\n"); err != nil {
		return err
	}
	for _, warning := range response.Warnings {
		if _, err := fmt.Fprintf(writer, "- %s: %s\n", warning.Path, warning.Message); err != nil {
			return err
		}
	}
	return nil
}

func writeHumanUpdateFile(writer io.Writer, response app.UpdateFileResponse) error {
	switch response.Status {
	case app.UpdateFileStatusUpdated:
		_, err := fmt.Fprintf(writer, "Updated file index\nPath: %s\nDeleted entries: %d\nIndexed entries: %d\n", response.Path, response.DeletedEntryCount, response.IndexedEntryCount)
		return err
	case app.UpdateFileStatusDeleted:
		_, err := fmt.Fprintf(writer, "Cleaned stale file index\nPath: %s\nDeleted entries: %d\n", response.Path, response.DeletedEntryCount)
		return err
	case app.UpdateFileStatusSkipped:
		_, err := fmt.Fprintf(writer, "Skipped file index update\nPath: %s\nReason: %s\n", response.Path, humanSkipReason(response.SkipReason))
		return err
	default:
		_, err := fmt.Fprintf(writer, "Updated file index\nPath: %s\nDeleted entries: %d\nIndexed entries: %d\n", response.Path, response.DeletedEntryCount, response.IndexedEntryCount)
		return err
	}
}

func humanSkipReason(reason app.UpdateFileSkipReason) string {
	switch reason {
	case app.UpdateFileSkipReasonOutsideCorpus:
		return "file is outside the configured corpus"
	case app.UpdateFileSkipReasonNotIndexed:
		return "missing file had no indexed entries"
	default:
		return string(reason)
	}
}

func writeError(writer io.Writer, err error, jsonOutput bool) {
	var duplicateErr projection.DuplicateIDsError
	if errors.As(err, &duplicateErr) {
		if jsonOutput {
			_ = writeJSON(writer, duplicateIDsErrorResponse{Error: duplicateErr.Error(), Duplicates: duplicateErr.Duplicates})
			return
		}
		writeHumanDuplicateIDError(writer, duplicateErr)
		return
	}

	if jsonOutput {
		_ = writeJSON(writer, map[string]string{"error": err.Error()})
		return
	}
	_, _ = fmt.Fprintf(writer, "Error: %s\n", err)
}

func writeHumanDuplicateIDError(writer io.Writer, err projection.DuplicateIDsError) {
	_, _ = fmt.Fprintf(writer, "Error: found %d duplicate Org IDs\n", len(err.Duplicates))
	for _, duplicate := range err.Duplicates {
		_, _ = fmt.Fprintf(writer, "- %s\n", duplicate.ID)
		for _, occurrence := range duplicate.Occurrences {
			if occurrence.Headline == "" {
				_, _ = fmt.Fprintf(writer, "  - %s\n", occurrence.Path)
				continue
			}
			_, _ = fmt.Fprintf(writer, "  - %s: %s\n", occurrence.Path, occurrence.Headline)
		}
	}
}

type duplicateIDsErrorResponse struct {
	Error      string                   `json:"error"`
	Duplicates []projection.DuplicateID `json:"duplicates"`
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
