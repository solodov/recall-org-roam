package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"org-search/internal/app"
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
	case app.SearchResponse:
		return writeHumanSearch(writer, value)
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
	_, err := fmt.Fprintf(writer, "Updated file index\nPath: %s\nDeleted entries: %d\nIndexed entries: %d\n", response.Path, response.DeletedEntryCount, response.IndexedEntryCount)
	return err
}

func writeHumanSearch(writer io.Writer, response app.SearchResponse) error {
	if len(response.Hits) == 0 {
		_, err := io.WriteString(writer, "No matches\n")
		return err
	}
	if _, err := fmt.Fprintf(writer, "%d matches\n", len(response.Hits)); err != nil {
		return err
	}
	for index, hit := range response.Hits {
		if _, err := fmt.Fprintf(writer, "%d. %s: %s\n", index+1, hit.ID, hit.Headline); err != nil {
			return err
		}
	}
	return nil
}

func writeError(writer io.Writer, err error, jsonOutput bool) {
	if jsonOutput {
		_ = writeJSON(writer, map[string]string{"error": err.Error()})
		return
	}
	_, _ = fmt.Fprintf(writer, "Error: %s\n", err)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
