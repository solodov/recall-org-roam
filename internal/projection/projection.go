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
	"unicode"

	goorg "github.com/niklasfasching/go-org/org"

	"org-search/internal/discovery"
	"org-search/internal/taghierarchy"
)

// EntryDocument stores one indexable Org entry projected from a parsed subtree.
type EntryDocument struct {
	ID                   string
	Path                 string
	CanonicalPath        string
	ParentID             string
	AncestorIDs          []string
	Outline              string
	Headline             string
	Todo                 string
	IsDone               bool
	IsArchived           bool
	Tags                 []string
	Style                string
	Category             string
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
func ProjectFile(path string, hierarchy taghierarchy.Hierarchy) ([]EntryDocument, error) {
	return ProjectPaths([]string{path}, hierarchy)
}

// ProjectPaths projects one corpus-worth of Org files and rejects every duplicate Org ID it finds.
func ProjectPaths(paths []string, hierarchy taghierarchy.Hierarchy) ([]EntryDocument, error) {
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

		fileDocuments, err := projectCanonicalFile(canonicalPath, visiblePath, hierarchy)
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

func projectCanonicalFile(canonicalPath string, visiblePath string, hierarchy taghierarchy.Hierarchy) ([]EntryDocument, error) {
	raw, err := os.ReadFile(canonicalPath)
	if err != nil {
		return nil, fmt.Errorf("read org file %q: %w", canonicalPath, err)
	}

	document := goorg.New().Silent().Parse(bytes.NewReader(raw), canonicalPath)
	if document.Error != nil {
		return nil, fmt.Errorf("parse org file %q: %w", canonicalPath, document.Error)
	}

	projected := make([]EntryDocument, 0)
	todoKeywords := collectTodoKeywords(todoKeywordSetting(document))
	doneKeywords := collectDoneKeywords(todoKeywordSetting(document))
	fileCategory := fileCategory(document)
	fileTags := parseFileTags(bufferSetting(document, "FILETAGS"))
	rootEntry, rootContext := projectFileRootEntry(document, visiblePath, canonicalPath, fileCategory, fileTags, hierarchy)
	if rootEntry.ID != "" {
		projected = append(projected, rootEntry)
	}
	for _, section := range document.Outline.Children {
		collectSectionDocuments(section, visiblePath, canonicalPath, fileCategory, fileTags, false, todoKeywords, doneKeywords, hierarchy, rootContext.outline, rootContext.ancestorIDs, rootContext.parentID, &projected)
	}
	return projected, nil
}

type hierarchyContext struct {
	outline     []string
	ancestorIDs []string
	parentID    string
}

func projectFileRootEntry(document *goorg.Document, visiblePath string, canonicalPath string, category string, fileTags []string, hierarchy taghierarchy.Hierarchy) (EntryDocument, hierarchyContext) {
	properties := fileProperties(document)
	if properties == nil {
		return EntryDocument{}, hierarchyContext{}
	}

	rootID, ok := properties.Get("ID")
	rootID = strings.TrimSpace(rootID)
	if !ok || rootID == "" {
		return EntryDocument{}, hierarchyContext{}
	}

	headline := fileHeadline(document, visiblePath)
	outline := []string{outlineSegment(headline)}
	entry := EntryDocument{
		ID:            rootID,
		Path:          visiblePath,
		CanonicalPath: canonicalPath,
		Outline:       strings.Join(outline, " / "),
		Headline:      headline,
		IsArchived:    hasArchiveTag(fileTags),
		Tags:          hierarchy.Expand(fileTags),
		Style:         fileStyle(document),
		Category:      category,
		Body:          fileRootBody(document),
	}
	return entry, hierarchyContext{outline: outline, ancestorIDs: []string{rootID}, parentID: rootID}
}

func collectSectionDocuments(section *goorg.Section, visiblePath string, canonicalPath string, inheritedCategory string, inheritedTags []string, inheritedArchived bool, todoKeywords []string, doneKeywords map[string]struct{}, hierarchy taghierarchy.Hierarchy, inheritedOutline []string, inheritedAncestorIDs []string, immediateParentID string, projected *[]EntryDocument) {
	if section == nil || section.Headline == nil {
		return
	}

	directBodyNodes := filterDirectBodyNodes(section.Headline.Children)
	properties, directBodyNodes := extractSectionProperties(section.Headline.Properties, directBodyNodes)
	category := inheritedCategory
	if propertyCategory, ok := properties.Get("CATEGORY"); ok && strings.TrimSpace(propertyCategory) != "" {
		category = strings.TrimSpace(propertyCategory)
	}
	rawTags := inheritTags(inheritedTags, section.Headline.Tags)
	expandedTags := hierarchy.Expand(rawTags)
	archived := inheritedArchived || hasArchiveTag(rawTags)
	planning := extractPlanningMetadata(directBodyNodes)
	status, headline := projectedHeadlineMetadata(section.Headline, todoKeywords)
	outlineSegments := appendStringCopy(inheritedOutline, outlineSegment(headline))
	outline := strings.Join(outlineSegments, " / ")
	currentID := ""
	if id, ok := properties.Get("ID"); ok {
		currentID = strings.TrimSpace(id)
		if currentID != "" {
			*projected = append(*projected, EntryDocument{
				ID:                   currentID,
				Path:                 visiblePath,
				CanonicalPath:        canonicalPath,
				ParentID:             immediateParentID,
				AncestorIDs:          appendStringCopy(nil, inheritedAncestorIDs...),
				Outline:              outline,
				Headline:             headline,
				Todo:                 status,
				IsDone:               isDoneStatus(status, doneKeywords),
				IsArchived:           archived,
				Tags:                 expandedTags,
				Style:                propertyValue(properties, "STYLE"),
				Category:             category,
				ScheduledDate:        planning.scheduledDate,
				ScheduledMinuteOfDay: planning.scheduledMinuteOfDay,
				DeadlineDate:         planning.deadlineDate,
				DeadlineMinuteOfDay:  planning.deadlineMinuteOfDay,
				Body:                 strings.TrimSpace(goorg.String(directBodyNodes...)),
			})
		}
	}

	childAncestorIDs := inheritedAncestorIDs
	if currentID != "" {
		childAncestorIDs = appendStringCopy(inheritedAncestorIDs, currentID)
	}
	childParentID := ""
	if currentID != "" {
		childParentID = currentID
	}
	for _, child := range section.Children {
		collectSectionDocuments(child, visiblePath, canonicalPath, category, rawTags, archived, todoKeywords, doneKeywords, hierarchy, outlineSegments, childAncestorIDs, childParentID, projected)
	}
}

func outlineSegment(headline string) string {
	trimmedHeadline := strings.TrimSpace(headline)
	if trimmedHeadline == "" {
		return "(untitled)"
	}
	return trimmedHeadline
}

func appendStringCopy(base []string, values ...string) []string {
	combined := append([]string(nil), base...)
	combined = append(combined, values...)
	return combined
}

func inheritTags(inheritedTags []string, localTags []string) []string {
	combined := make([]string, 0, len(inheritedTags)+len(localTags))
	seen := make(map[string]struct{}, len(inheritedTags)+len(localTags))
	for _, tag := range inheritedTags {
		trimmedTag := strings.TrimSpace(tag)
		if trimmedTag == "" {
			continue
		}
		if _, ok := seen[trimmedTag]; ok {
			continue
		}
		seen[trimmedTag] = struct{}{}
		combined = append(combined, trimmedTag)
	}
	for _, tag := range localTags {
		trimmedTag := strings.TrimSpace(tag)
		if trimmedTag == "" {
			continue
		}
		if _, ok := seen[trimmedTag]; ok {
			continue
		}
		seen[trimmedTag] = struct{}{}
		combined = append(combined, trimmedTag)
	}
	return combined
}

func fileProperties(document *goorg.Document) *goorg.PropertyDrawer {
	for _, node := range document.Nodes {
		switch node := node.(type) {
		case goorg.Headline:
			return nil
		case goorg.PropertyDrawer:
			drawerCopy := node
			return &drawerCopy
		}
	}
	return nil
}

func fileHeadline(document *goorg.Document, path string) string {
	if title := strings.TrimSpace(bufferSetting(document, "TITLE")); title != "" {
		return title
	}
	baseName := filepath.Base(path)
	return strings.TrimSuffix(baseName, filepath.Ext(baseName))
}

func fileRootBody(document *goorg.Document) string {
	bodyNodes := make([]goorg.Node, 0)
	for _, node := range document.Nodes {
		switch node.(type) {
		case goorg.Headline:
			return strings.TrimSpace(goorg.String(bodyNodes...))
		case goorg.PropertyDrawer, goorg.Keyword:
			continue
		default:
			bodyNodes = append(bodyNodes, node)
		}
	}
	return strings.TrimSpace(goorg.String(bodyNodes...))
}

func hasArchiveTag(tags []string) bool {
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "ARCHIVE" {
			return true
		}
	}
	return false
}

func parseFileTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	tags := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(raw, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		var lineTags []string
		if strings.Contains(trimmedLine, ":") {
			parts := strings.Split(trimmedLine, ":")
			lineTags = make([]string, 0, len(parts))
			for _, part := range parts {
				trimmedPart := strings.TrimSpace(part)
				if trimmedPart != "" {
					lineTags = append(lineTags, trimmedPart)
				}
			}
		} else {
			lineTags = strings.Fields(trimmedLine)
		}

		for _, tag := range lineTags {
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	return tags
}

func fileStyle(document *goorg.Document) string {
	return propertyValue(fileProperties(document), "STYLE")
}

func fileCategory(document *goorg.Document) string {
	if value := bufferSetting(document, "CATEGORY"); value != "" {
		return value
	}
	return propertyValue(fileProperties(document), "CATEGORY")
}

func propertyValue(properties *goorg.PropertyDrawer, key string) string {
	if value, ok := properties.Get(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func projectedHeadlineMetadata(headline *goorg.Headline, todoKeywords []string) (string, string) {
	rawHeadline := strings.TrimSpace(goorg.String(headline.Title...))
	status := strings.TrimSpace(headline.Status)
	if status == "" {
		status, rawHeadline = extractTodoStatus(rawHeadline, todoKeywords)
	}
	return status, stripPriorityPrefix(rawHeadline)
}

func extractTodoStatus(headline string, todoKeywords []string) (string, string) {
	for _, keyword := range todoKeywords {
		if !strings.HasPrefix(headline, keyword) || len(headline) == len(keyword) {
			continue
		}
		if !unicode.IsSpace(rune(headline[len(keyword)])) {
			continue
		}
		return keyword, strings.TrimSpace(headline[len(keyword):])
	}
	return "", headline
}

func stripPriorityPrefix(headline string) string {
	if len(headline) >= 4 && strings.HasPrefix(headline, "[#") && headline[3] == ']' {
		return strings.TrimSpace(headline[4:])
	}
	return headline
}

func todoKeywordSetting(document *goorg.Document) string {
	if value := bufferSetting(document, "TODO"); value != "" {
		return value
	}
	return document.Get("TODO")
}

func bufferSetting(document *goorg.Document, key string) string {
	if value := strings.TrimSpace(document.BufferSettings[key]); value != "" {
		return value
	}
	lowercaseKey := strings.ToLower(key)
	if value := strings.TrimSpace(document.BufferSettings[lowercaseKey]); value != "" {
		return value
	}
	uppercaseKey := strings.ToUpper(key)
	if value := strings.TrimSpace(document.BufferSettings[uppercaseKey]); value != "" {
		return value
	}
	return ""
}

func collectTodoKeywords(raw string) []string {
	todoKeywords := make([]string, 0)
	for _, token := range strings.Fields(raw) {
		if token == "|" {
			continue
		}
		trimmedKeyword := trimTodoKeyword(token)
		if trimmedKeyword == "" {
			continue
		}
		todoKeywords = append(todoKeywords, trimmedKeyword)
	}
	return todoKeywords
}

func collectDoneKeywords(raw string) map[string]struct{} {
	doneKeywords := make(map[string]struct{})
	inDoneSection := false
	for _, token := range strings.Fields(raw) {
		if token == "|" {
			inDoneSection = true
			continue
		}
		if !inDoneSection {
			continue
		}
		trimmedKeyword := trimTodoKeyword(token)
		if trimmedKeyword == "" {
			continue
		}
		doneKeywords[trimmedKeyword] = struct{}{}
	}
	return doneKeywords
}

func isDoneStatus(status string, doneKeywords map[string]struct{}) bool {
	trimmedStatus := strings.TrimSpace(status)
	if trimmedStatus == "" {
		return false
	}
	_, ok := doneKeywords[trimmedStatus]
	return ok
}

func trimTodoKeyword(keyword string) string {
	trimmedKeyword := strings.TrimSpace(keyword)
	leftParen := strings.LastIndex(trimmedKeyword, "(")
	rightParen := strings.LastIndex(trimmedKeyword, ")")
	if leftParen != -1 && rightParen == len(trimmedKeyword)-1 && leftParen < rightParen {
		return trimmedKeyword[:leftParen]
	}
	return trimmedKeyword
}

func extractSectionProperties(headlineProperties *goorg.PropertyDrawer, nodes []goorg.Node) (*goorg.PropertyDrawer, []goorg.Node) {
	properties := headlineProperties
	filtered := make([]goorg.Node, 0, len(nodes))
	for _, node := range nodes {
		drawer, ok := node.(goorg.PropertyDrawer)
		if !ok {
			filtered = append(filtered, node)
			continue
		}
		if properties == nil {
			drawerCopy := drawer
			properties = &drawerCopy
		}
	}
	return properties, filtered
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
