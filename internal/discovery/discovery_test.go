package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverFollowsReachableSymlinkedDirectoriesAndFilesWithoutDuplicates(t *testing.T) {
	t.Helper()

	rootDir := t.TempDir()
	outsideDir := t.TempDir()

	insidePath := writeTestFile(t, filepath.Join(rootDir, "inside.org"))
	sharedPath := writeTestFile(t, filepath.Join(outsideDir, "shared.org"))
	outsideOnlyPath := writeTestFile(t, filepath.Join(outsideDir, "outside-only.org"))
	writeTestFile(t, filepath.Join(outsideDir, "skip.txt"))
	mustSymlink(t, outsideDir, filepath.Join(rootDir, "outside-link"))
	mustSymlink(t, sharedPath, filepath.Join(rootDir, "shared-link.org"))

	result, err := Discover(rootDir)
	if err != nil {
		t.Fatalf("discover notes root: %v", err)
	}

	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", result.Warnings)
	}
	assertSamePaths(t, result.Paths, []string{insidePath, outsideOnlyPath, sharedPath})
}

func TestDiscoverAvoidsSymlinkLoops(t *testing.T) {
	t.Helper()

	rootDir := t.TempDir()
	loopTarget := filepath.Join(rootDir, "loop-target")
	if err := os.MkdirAll(loopTarget, 0o755); err != nil {
		t.Fatalf("create loop target: %v", err)
	}
	insidePath := writeTestFile(t, filepath.Join(loopTarget, "inside.org"))
	mustSymlink(t, rootDir, filepath.Join(loopTarget, "back-to-root"))

	result, err := Discover(rootDir)
	if err != nil {
		t.Fatalf("discover notes root: %v", err)
	}

	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", result.Warnings)
	}
	assertSamePaths(t, result.Paths, []string{insidePath})
}

func TestDiscoverSurfacesBrokenSymlinksAndUnreadableReachablePaths(t *testing.T) {
	t.Helper()

	rootDir := t.TempDir()
	canonicalRootDir := mustCanonicalizePath(t, rootDir)
	visiblePath := writeTestFile(t, filepath.Join(rootDir, "visible.org"))
	mustSymlink(t, filepath.Join(rootDir, "missing.org"), filepath.Join(rootDir, "broken.org"))

	unreadableFilePath := writeTestFile(t, filepath.Join(rootDir, "private.org"))
	if err := os.Chmod(unreadableFilePath, 0); err != nil {
		t.Fatalf("chmod unreadable file: %v", err)
	}
	defer func() {
		_ = os.Chmod(unreadableFilePath, 0o600)
	}()

	lockedDirPath := filepath.Join(rootDir, "locked")
	if err := os.MkdirAll(lockedDirPath, 0o755); err != nil {
		t.Fatalf("create locked directory: %v", err)
	}
	writeTestFile(t, filepath.Join(lockedDirPath, "hidden.org"))
	if err := os.Chmod(lockedDirPath, 0); err != nil {
		t.Fatalf("chmod locked directory: %v", err)
	}
	defer func() {
		_ = os.Chmod(lockedDirPath, 0o755)
	}()

	result, err := Discover(rootDir)
	if err != nil {
		t.Fatalf("discover notes root: %v", err)
	}

	assertSamePaths(t, result.Paths, []string{visiblePath})
	if len(result.Warnings) != 3 {
		t.Fatalf("warnings = %+v, want 3 warnings", result.Warnings)
	}
	assertWarningPathContains(t, result.Warnings, filepath.Join(canonicalRootDir, "broken.org"), "resolve symlinks")
	assertWarningPathContains(t, result.Warnings, unreadableFilePath, "open file")
	assertWarningPathContains(t, result.Warnings, filepath.Join(canonicalRootDir, "locked"), "read directory")
}

func TestCanonicalizePathResolvesMissingLeafThroughExistingParentSymlinks(t *testing.T) {
	t.Helper()

	rootDir := t.TempDir()
	outsideDir := mustCanonicalizePath(t, t.TempDir())
	mustSymlink(t, outsideDir, filepath.Join(rootDir, "outside-link"))

	canonicalPath, err := CanonicalizePath(filepath.Join(rootDir, "outside-link", "missing.org"))
	if err != nil {
		t.Fatalf("canonicalize missing path: %v", err)
	}

	if got, want := canonicalPath, filepath.Join(outsideDir, "missing.org"); got != want {
		t.Fatalf("canonicalPath = %q, want %q", got, want)
	}
}

func writeTestFile(t *testing.T, path string) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("* heading\n"), 0o600); err != nil {
		t.Fatalf("write file %q: %v", path, err)
	}
	return mustCanonicalizePath(t, path)
}

func mustSymlink(t *testing.T, target string, path string) {
	t.Helper()

	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink %q -> %q: %v", path, target, err)
	}
}

func mustCanonicalizePath(t *testing.T, path string) string {
	t.Helper()

	canonicalPath, err := CanonicalizePath(path)
	if err != nil {
		t.Fatalf("canonicalize %q: %v", path, err)
	}
	return canonicalPath
}

func assertSamePaths(t *testing.T, got []string, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	}
}

func assertWarningPathContains(t *testing.T, warnings []Warning, wantPath string, wantMessageSubstring string) {
	t.Helper()

	for _, warning := range warnings {
		if warning.Path == wantPath && strings.Contains(warning.Message, wantMessageSubstring) {
			return
		}
	}
	t.Fatalf("warnings = %+v, want path %q containing %q", warnings, wantPath, wantMessageSubstring)
}
