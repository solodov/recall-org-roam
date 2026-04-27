package taghierarchy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadParsesHierarchyFromTagsOrg(t *testing.T) {
	t.Helper()

	rootDir := t.TempDir()
	writeTagsFile(t, filepath.Join(rootDir, "tags.org"), `#+TAGS: [ broad : focused ]
#+TAGS: [ root : broad ]
#+TAGS: { delegated : @one @two }
#+TAGS: standalone
`)

	hierarchy, err := Load(rootDir)
	if err != nil {
		t.Fatalf("load hierarchy: %v", err)
	}

	if got, want := hierarchy.Expand([]string{"focused", "@one", "standalone"}), []string{"@one", "broad", "delegated", "focused", "root", "standalone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded tags = %#v, want %#v", got, want)
	}
}

func TestExpandMatchesRegexGroupMembers(t *testing.T) {
	t.Helper()

	hierarchy, err := Parse([]byte(`#+TAGS: [ project : {P@.+} ]`), "tags.org")
	if err != nil {
		t.Fatalf("parse hierarchy: %v", err)
	}

	if got, want := hierarchy.Expand([]string{"P@2026_demo"}), []string{"P@2026_demo", "project"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded tags = %#v, want %#v", got, want)
	}
}

func TestLoadMissingTagsOrgReturnsEmptyHierarchy(t *testing.T) {
	t.Helper()

	hierarchy, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("load hierarchy: %v", err)
	}

	if got, want := hierarchy.Expand([]string{"alpha"}), []string{"alpha"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded tags = %#v, want %#v", got, want)
	}
}

func writeTagsFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create tag hierarchy directory for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write tag hierarchy file %q: %v", path, err)
	}
}
