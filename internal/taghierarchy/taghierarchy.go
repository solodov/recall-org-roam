// Package taghierarchy loads Org tag group definitions from tags.org and expands entry tags through that hierarchy.
package taghierarchy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	goorg "github.com/niklasfasching/go-org/org"
)

const fileName = "tags.org"

// Hierarchy expands explicit Org tags into the full set of ancestor group tags.
type Hierarchy struct {
	parentsByTag stringMultiMap
	regexParents []regexParent
}

// Load reads the default tags.org hierarchy file from one notes root.
// Missing tags.org files are treated as an empty hierarchy.
func Load(notesRoot string) (Hierarchy, error) {
	path := FilePath(notesRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Hierarchy{}, nil
		}
		return Hierarchy{}, fmt.Errorf("read tag hierarchy %q: %w", path, err)
	}
	return Parse(raw, path)
}

// FilePath returns the default tags.org hierarchy file path for one notes root.
func FilePath(notesRoot string) string {
	return filepath.Join(notesRoot, fileName)
}

// Parse builds one hierarchy from the raw contents of a tags.org-like Org file.
func Parse(raw []byte, path string) (Hierarchy, error) {
	document := goorg.New().Silent().Parse(bytes.NewReader(raw), path)
	if document.Error != nil {
		return Hierarchy{}, fmt.Errorf("parse tag hierarchy %q: %w", path, document.Error)
	}

	hierarchy := Hierarchy{parentsByTag: stringMultiMap{}}
	for _, line := range strings.Split(bufferSetting(document, "TAGS"), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if err := hierarchy.addDefinition(trimmedLine); err != nil {
			return Hierarchy{}, fmt.Errorf("parse tag hierarchy %q: %w", path, err)
		}
	}
	return hierarchy, nil
}

// Expand returns the explicit tags plus every ancestor group tag implied by the hierarchy.
func (hierarchy Hierarchy) Expand(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}

	expanded := make(map[string]struct{}, len(tags))
	queue := make([]string, 0, len(tags))
	for _, tag := range tags {
		trimmedTag := strings.TrimSpace(tag)
		if trimmedTag == "" {
			continue
		}
		if _, seen := expanded[trimmedTag]; seen {
			continue
		}
		expanded[trimmedTag] = struct{}{}
		queue = append(queue, trimmedTag)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, parent := range hierarchy.directParents(current) {
			if _, seen := expanded[parent]; seen {
				continue
			}
			expanded[parent] = struct{}{}
			queue = append(queue, parent)
		}
	}

	result := make([]string, 0, len(expanded))
	for tag := range expanded {
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

type regexParent struct {
	pattern *regexp.Regexp
	parent  string
}

type stringMultiMap map[string][]string

func (hierarchy *Hierarchy) addDefinition(line string) error {
	groupTag, members, isGroup, err := parseDefinition(line)
	if err != nil {
		return err
	}
	if !isGroup {
		return nil
	}

	for _, member := range members {
		if member.isPattern {
			compiledPattern, err := regexp.Compile(member.value)
			if err != nil {
				return fmt.Errorf("compile tag group pattern %q: %w", member.value, err)
			}
			hierarchy.regexParents = append(hierarchy.regexParents, regexParent{pattern: compiledPattern, parent: groupTag})
			continue
		}
		hierarchy.parentsByTag.add(member.value, groupTag)
	}
	return nil
}

func (hierarchy Hierarchy) directParents(tag string) []string {
	parents := append([]string(nil), hierarchy.parentsByTag[tag]...)
	for _, regexParent := range hierarchy.regexParents {
		if !regexParent.pattern.MatchString(tag) {
			continue
		}
		parents = appendUniqueString(parents, regexParent.parent)
	}
	return parents
}

type tagDefinitionMember struct {
	value     string
	isPattern bool
}

func parseDefinition(line string) (string, []tagDefinitionMember, bool, error) {
	if line == "" {
		return "", nil, false, nil
	}
	opening, closing, isGroup := groupDelimiters(line)
	if !isGroup {
		return "", nil, false, nil
	}

	trimmedLine := strings.TrimSpace(line)
	inner := strings.TrimSpace(trimmedLine[1 : len(trimmedLine)-1])
	groupTagText, membersText, hasSeparator := strings.Cut(inner, ":")
	if !hasSeparator {
		return "", nil, false, fmt.Errorf("group tag definition %q is missing ':' separator", line)
	}
	groupTag := singleTagToken(groupTagText)
	if groupTag == "" {
		return "", nil, false, fmt.Errorf("group tag definition %q requires one group tag", line)
	}

	memberTokens := strings.Fields(strings.TrimSpace(membersText))
	members := make([]tagDefinitionMember, 0, len(memberTokens))
	for _, memberToken := range memberTokens {
		memberToken = strings.TrimSpace(memberToken)
		if memberToken == "" {
			continue
		}
		if strings.HasPrefix(memberToken, "{") && strings.HasSuffix(memberToken, "}") && len(memberToken) >= 2 {
			pattern := strings.TrimSpace(memberToken[1 : len(memberToken)-1])
			if pattern == "" {
				return "", nil, false, fmt.Errorf("group tag definition %q has an empty pattern member", line)
			}
			members = append(members, tagDefinitionMember{value: pattern, isPattern: true})
			continue
		}
		members = append(members, tagDefinitionMember{value: memberToken})
	}
	if len(members) == 0 {
		return "", nil, false, fmt.Errorf("group tag definition %q requires at least one member", line)
	}

	if trimmedLine[0] != opening || trimmedLine[len(trimmedLine)-1] != closing {
		return "", nil, false, fmt.Errorf("malformed group tag definition %q", line)
	}
	return groupTag, members, true, nil
}

func groupDelimiters(line string) (byte, byte, bool) {
	trimmedLine := strings.TrimSpace(line)
	if len(trimmedLine) < 2 {
		return 0, 0, false
	}
	switch trimmedLine[0] {
	case '[':
		if trimmedLine[len(trimmedLine)-1] == ']' {
			return '[', ']', true
		}
	case '{':
		if trimmedLine[len(trimmedLine)-1] == '}' {
			return '{', '}', true
		}
	}
	return 0, 0, false
}

func singleTagToken(raw string) string {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) != 1 {
		return ""
	}
	return fields[0]
}

func (multiMap stringMultiMap) add(key string, value string) {
	multiMap[key] = appendUniqueString(multiMap[key], value)
}

func appendUniqueString(values []string, value string) []string {
	for _, existingValue := range values {
		if existingValue == value {
			return values
		}
	}
	return append(values, value)
}

func bufferSetting(document *goorg.Document, key string) string {
	if value := strings.TrimSpace(document.BufferSettings[key]); value != "" {
		return value
	}
	uppercaseKey := strings.ToUpper(key)
	if value := strings.TrimSpace(document.BufferSettings[uppercaseKey]); value != "" {
		return value
	}
	lowercaseKey := strings.ToLower(key)
	if value := strings.TrimSpace(document.BufferSettings[lowercaseKey]); value != "" {
		return value
	}
	return ""
}
