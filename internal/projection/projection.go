// Package projection turns Org files into org-search entry documents without exposing go-org types.
package projection

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	goorg "github.com/niklasfasching/go-org/org"

	"org-search/internal/discovery"
)

// EntryDocument stores one indexable Org entry projected from a parsed subtree.
type EntryDocument struct {
	ID                   string
	Path                 string
	CanonicalPath        string
	Headline             string
	Todo                 string
	ScheduledDate        string
	ScheduledMinuteOfDay *int
	DeadlineDate         string
	DeadlineMinuteOfDay  *int
	Body                 string
}

// DuplicateIDOccurrence stores one duplicate Org ID occurrence that the operator can inspect and fix.
type DuplicateIDOccurrence struct {
	Path     string `json:"path"`
	Headline string `json:"headline,omitempty"`
}

// DuplicateID stores every occurrence for one duplicate Org ID.
type DuplicateID struct {
	ID          string                  `json:"id"`
	Occurrences []DuplicateIDOccurrence `json:"occurrences"`
}

// DuplicateIDsError reports every duplicate Org ID found while projecting one or more files.
type DuplicateIDsError struct {
	Duplicates []DuplicateID `json:"duplicates"`
}

func (err DuplicateIDsError) Error() string {
	if len(err.Duplicates) == 0 {
		return "found duplicate org IDs"
	}

	parts := make([]string, 0, len(err.Duplicates))
	for _, duplicate := range err.Duplicates {
		paths := make([]string, 0, len(duplicate.Occurrences))
		for _, occurrence := range duplicate.Occurrences {
			paths = append(paths, occurrence.Path)
		}
		parts = append(parts, fmt.Sprintf("%q in %s", duplicate.ID, strings.Join(paths, ", ")))
	}
	return fmt.Sprintf("found %d duplicate org IDs: %s", len(err.Duplicates), strings.Join(parts, "; "))
}

// ProjectFile projects one Org file into entry documents keyed by Org ID.
func ProjectFile(path string) ([]EntryDocument, error) {
	return ProjectPaths([]string{path})
}

// ProjectPaths projects one corpus-worth of Org files and rejects every duplicate Org ID it finds.
func ProjectPaths(paths []string) ([]EntryDocument, error) {
	projected := make([]EntryDocument, 0)
	seenPaths := make(map[string]struct{}, len(paths))
	occurrencesByID := make(map[string][]DuplicateIDOccurrence)
	for _, path := range paths {
		visiblePath, err := absolutePath(path)
		if err != nil {
			return nil, fmt.Errorf("normalize org file %q: %w", path, err)
		}
		canonicalPath, err := discovery.CanonicalizePath(path)
		if err != nil {
			return nil, fmt.Errorf("canonicalize org file %q: %w", path, err)
		}
		if _, seen := seenPaths[canonicalPath]; seen {
			continue
		}
		seenPaths[canonicalPath] = struct{}{}

		fileDocuments, err := projectCanonicalFile(canonicalPath, visiblePath)
		if err != nil {
			return nil, err
		}
		for _, document := range fileDocuments {
			projected = append(projected, document)
			occurrencesByID[document.ID] = append(occurrencesByID[document.ID], DuplicateIDOccurrence{Path: document.Path, Headline: document.Headline})
		}
	}

	duplicates := collectDuplicateIDs(occurrencesByID)
	if len(duplicates) > 0 {
		return nil, DuplicateIDsError{Duplicates: duplicates}
	}
	return projected, nil
}

func projectCanonicalFile(canonicalPath string, visiblePath string) ([]EntryDocument, error) {
	raw, err := os.ReadFile(canonicalPath)
	if err != nil {
		return nil, fmt.Errorf("read org file %q: %w", canonicalPath, err)
	}

	document := goorg.New().Silent().Parse(bytes.NewReader(raw), canonicalPath)
	if document.Error != nil {
		return nil, fmt.Errorf("parse org file %q: %w", canonicalPath, document.Error)
	}

	projected := make([]EntryDocument, 0)
	for _, section := range document.Outline.Children {
		collectSectionDocuments(section, visiblePath, canonicalPath, &projected)
	}
	return projected, nil
}

func collectSectionDocuments(section *goorg.Section, visiblePath string, canonicalPath string, projected *[]EntryDocument) {
	if section == nil || section.Headline == nil {
		return
	}

	directBodyNodes := filterDirectBodyNodes(section.Headline.Children)
	planning := extractPlanningMetadata(directBodyNodes)
	if id, ok := section.Headline.Properties.Get("ID"); ok {
		trimmedID := strings.TrimSpace(id)
		if trimmedID != "" {
			*projected = append(*projected, EntryDocument{
				ID:                   trimmedID,
				Path:                 visiblePath,
				CanonicalPath:        canonicalPath,
				Headline:             strings.TrimSpace(goorg.String(section.Headline.Title...)),
				Todo:                 strings.TrimSpace(section.Headline.Status),
				ScheduledDate:        planning.scheduledDate,
				ScheduledMinuteOfDay: planning.scheduledMinuteOfDay,
				DeadlineDate:         planning.deadlineDate,
				DeadlineMinuteOfDay:  planning.deadlineMinuteOfDay,
				Body:                 strings.TrimSpace(goorg.String(directBodyNodes...)),
			})
		}
	}

	for _, child := range section.Children {
		collectSectionDocuments(child, visiblePath, canonicalPath, projected)
	}
}

func extractPlanningMetadata(nodes []goorg.Node) planningMetadata {
	if len(nodes) == 0 {
		return planningMetadata{}
	}
	firstParagraph, ok := nodes[0].(goorg.Paragraph)
	if !ok {
		return planningMetadata{}
	}

	metadata := planningMetadata{}
	for _, line := range strings.Split(goorg.String(firstParagraph.Children...), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		matches := planningLineRegexp.FindAllStringSubmatch(trimmedLine, -1)
		remainder := planningLineRegexp.ReplaceAllString(trimmedLine, "")
		remainder = closedPlanningLineRegexp.ReplaceAllString(remainder, "")
		if len(matches) == 0 || strings.TrimSpace(remainder) != "" {
			break
		}
		for _, match := range matches {
			date := match[2]
			minuteOfDay := planningMinuteOfDay(match[3], match[4])
			switch match[1] {
			case "SCHEDULED":
				metadata.scheduledDate = date
				metadata.scheduledMinuteOfDay = minuteOfDay
			case "DEADLINE":
				metadata.deadlineDate = date
				metadata.deadlineMinuteOfDay = minuteOfDay
			}
		}
	}
	return metadata
}

func planningMinuteOfDay(hourText string, minuteText string) *int {
	if hourText == "" || minuteText == "" {
		return nil
	}
	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return nil
	}
	minute, err := strconv.Atoi(minuteText)
	if err != nil {
		return nil
	}
	minuteOfDay := hour*60 + minute
	return &minuteOfDay
}

type planningMetadata struct {
	scheduledDate        string
	scheduledMinuteOfDay *int
	deadlineDate         string
	deadlineMinuteOfDay  *int
}

var (
	planningLineRegexp       = regexp.MustCompile(`(?:^|\s)(SCHEDULED|DEADLINE):\s*<(\d{4}-\d{2}-\d{2})(?:\s+[A-Za-z]+)?(?:\s+(\d{2}):(\d{2}))?[^>]*>`)
	closedPlanningLineRegexp = regexp.MustCompile(`(?:^|\s)CLOSED:\s*\[[^\]]+\]`)
)

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

func collectDuplicateIDs(occurrencesByID map[string][]DuplicateIDOccurrence) []DuplicateID {
	duplicates := make([]DuplicateID, 0)
	for id, occurrences := range occurrencesByID {
		if len(occurrences) < 2 {
			continue
		}
		sortedOccurrences := append([]DuplicateIDOccurrence(nil), occurrences...)
		sort.Slice(sortedOccurrences, func(left int, right int) bool {
			if sortedOccurrences[left].Path == sortedOccurrences[right].Path {
				return sortedOccurrences[left].Headline < sortedOccurrences[right].Headline
			}
			return sortedOccurrences[left].Path < sortedOccurrences[right].Path
		})
		duplicates = append(duplicates, DuplicateID{ID: id, Occurrences: sortedOccurrences})
	}
	sort.Slice(duplicates, func(left int, right int) bool {
		return duplicates[left].ID < duplicates[right].ID
	})
	return duplicates
}

func absolutePath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}
	return filepath.Clean(absolutePath), nil
}
