// Package projection turns Org files into org-search entry documents without exposing go-org types.
package projection

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	goorg "github.com/niklasfasching/go-org/org"

	"org-search/internal/discovery"
)

// EntryDocument stores one indexable Org entry projected from a parsed subtree.
type EntryDocument struct {
	ID       string
	Path     string
	Headline string
	Body     string
}

// DuplicateIDError reports that two reachable entries declared the same Org ID.
type DuplicateIDError struct {
	ID         string
	FirstPath  string
	SecondPath string
}

func (err DuplicateIDError) Error() string {
	return fmt.Sprintf("duplicate org ID %q in %q and %q", err.ID, err.FirstPath, err.SecondPath)
}

// ProjectFile projects one Org file into entry documents keyed by Org ID.
func ProjectFile(path string) ([]EntryDocument, error) {
	return ProjectPaths([]string{path})
}

// ProjectPaths projects one corpus-worth of Org files and rejects duplicate Org IDs.
func ProjectPaths(paths []string) ([]EntryDocument, error) {
	projected := make([]EntryDocument, 0)
	seenPaths := make(map[string]struct{}, len(paths))
	seenIDs := make(map[string]string)
	for _, path := range paths {
		canonicalPath, err := discovery.CanonicalizePath(path)
		if err != nil {
			return nil, fmt.Errorf("canonicalize org file %q: %w", path, err)
		}
		if _, seen := seenPaths[canonicalPath]; seen {
			continue
		}
		seenPaths[canonicalPath] = struct{}{}

		fileDocuments, err := projectCanonicalFile(canonicalPath)
		if err != nil {
			return nil, err
		}
		for _, document := range fileDocuments {
			if firstPath, exists := seenIDs[document.ID]; exists {
				return nil, DuplicateIDError{ID: document.ID, FirstPath: firstPath, SecondPath: document.Path}
			}
			seenIDs[document.ID] = document.Path
			projected = append(projected, document)
		}
	}
	return projected, nil
}

func projectCanonicalFile(path string) ([]EntryDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read org file %q: %w", path, err)
	}

	document := goorg.New().Silent().Parse(bytes.NewReader(raw), path)
	if document.Error != nil {
		return nil, fmt.Errorf("parse org file %q: %w", path, document.Error)
	}

	projected := make([]EntryDocument, 0)
	for _, section := range document.Outline.Children {
		collectSectionDocuments(section, path, &projected)
	}
	return projected, nil
}

func collectSectionDocuments(section *goorg.Section, path string, projected *[]EntryDocument) {
	if section == nil || section.Headline == nil {
		return
	}

	if id, ok := section.Headline.Properties.Get("ID"); ok {
		trimmedID := strings.TrimSpace(id)
		if trimmedID != "" {
			*projected = append(*projected, EntryDocument{
				ID:       trimmedID,
				Path:     path,
				Headline: strings.TrimSpace(goorg.String(section.Headline.Title...)),
				Body:     strings.TrimSpace(goorg.String(filterDirectBodyNodes(section.Headline.Children)...)),
			})
		}
	}

	for _, child := range section.Children {
		collectSectionDocuments(child, path, projected)
	}
}

func filterDirectBodyNodes(nodes []goorg.Node) []goorg.Node {
	filtered := make([]goorg.Node, 0, len(nodes))
	for _, node := range nodes {
		if _, isHeadline := node.(goorg.Headline); isHeadline {
			continue
		}
		filtered = append(filtered, node)
	}
	return filtered
}
