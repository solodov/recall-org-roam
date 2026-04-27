// Package discovery resolves reachable Org files into visible corpus paths plus canonical file identities.
package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// File stores one reachable Org file with its visible corpus path and canonical file identity.
type File struct {
	Path          string
	CanonicalPath string
}

// Result stores the visible Org file paths, canonical file identities, and non-fatal warnings from one discovery pass.
type Result struct {
	Paths    []string
	Files    []File
	Warnings []Warning
}

// Warning describes one non-fatal discovery problem for a reachable path.
type Warning struct {
	Path    string
	Message string
}

// Discover walks one notes root, follows reachable symlinks, and returns deduplicated visible Org file paths keyed by canonical identity.
func Discover(root string) (Result, error) {
	visibleRoot, err := absolutePath(root)
	if err != nil {
		return Result{}, fmt.Errorf("normalize notes root %q: %w", root, err)
	}
	canonicalRoot, err := CanonicalizePath(root)
	if err != nil {
		return Result{}, fmt.Errorf("canonicalize notes root %q: %w", root, err)
	}

	info, err := os.Stat(canonicalRoot)
	if err != nil {
		return Result{}, fmt.Errorf("stat notes root %q: %w", canonicalRoot, err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("notes root %q is not a directory", canonicalRoot)
	}

	walker := discoveryWalker{
		seenDirectories: map[string]struct{}{},
		seenFiles:       map[string]File{},
	}
	if err := walker.visitDirectory(visibleRoot, canonicalRoot, true); err != nil {
		return Result{}, err
	}
	return walker.result(), nil
}

// CanonicalizePath resolves one path into the canonical absolute identity used by discovery.
//
// Existing paths resolve through the full symlink chain. Missing leaf paths keep the
// unresolved leaf name but still resolve every existing parent directory so targeted
// updates can share the same file identity rules as full discovery.
func CanonicalizePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}

	resolvedPath, err := filepath.EvalSymlinks(absolutePath)
	if err == nil {
		return filepath.Clean(resolvedPath), nil
	}

	info, statErr := os.Lstat(absolutePath)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("resolve symlinks for %q: %w", absolutePath, err)
	}
	if !isNotExistError(err) {
		return "", fmt.Errorf("resolve symlinks for %q: %w", absolutePath, err)
	}

	resolvedMissingPath, err := canonicalizeMissingPath(absolutePath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolvedMissingPath), nil
}

type discoveryWalker struct {
	seenDirectories map[string]struct{}
	seenFiles       map[string]File
	warnings        []Warning
}

func (walker *discoveryWalker) result() Result {
	files := make([]File, 0, len(walker.seenFiles))
	for _, file := range walker.seenFiles {
		files = append(files, file)
	}
	sort.Slice(files, func(left int, right int) bool {
		if files[left].Path == files[right].Path {
			return files[left].CanonicalPath < files[right].CanonicalPath
		}
		return files[left].Path < files[right].Path
	})

	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}

	warnings := append([]Warning(nil), walker.warnings...)
	sort.Slice(warnings, func(left int, right int) bool {
		if warnings[left].Path == warnings[right].Path {
			return warnings[left].Message < warnings[right].Message
		}
		return warnings[left].Path < warnings[right].Path
	})

	return Result{Paths: paths, Files: files, Warnings: warnings}
}

func (walker *discoveryWalker) visitDirectory(visiblePath string, canonicalPath string, isRoot bool) error {
	canonicalPath = filepath.Clean(canonicalPath)
	if _, seen := walker.seenDirectories[canonicalPath]; seen {
		return nil
	}
	walker.seenDirectories[canonicalPath] = struct{}{}

	entries, err := os.ReadDir(canonicalPath)
	if err != nil {
		if isRoot {
			return fmt.Errorf("read notes root %q: %w", visiblePath, err)
		}
		walker.addWarning(visiblePath, fmt.Sprintf("read directory: %v", err))
		return nil
	}

	for _, entry := range entries {
		walker.visitPath(filepath.Join(visiblePath, entry.Name()), filepath.Join(canonicalPath, entry.Name()))
	}
	return nil
}

func (walker *discoveryWalker) visitPath(visiblePath string, canonicalPath string) {
	info, err := os.Lstat(visiblePath)
	if err != nil {
		walker.addWarning(visiblePath, fmt.Sprintf("inspect path: %v", err))
		return
	}

	if info.Mode()&os.ModeSymlink != 0 {
		walker.visitSymlink(visiblePath)
		return
	}
	if info.IsDir() {
		_ = walker.visitDirectory(visiblePath, canonicalPath, false)
		return
	}
	walker.visitFile(visiblePath, canonicalPath)
}

func (walker *discoveryWalker) visitSymlink(visiblePath string) {
	canonicalPath, err := CanonicalizePath(visiblePath)
	if err != nil {
		walker.addWarning(visiblePath, err.Error())
		return
	}

	info, err := os.Stat(canonicalPath)
	if err != nil {
		walker.addWarning(visiblePath, fmt.Sprintf("stat resolved path %q: %v", canonicalPath, err))
		return
	}
	if info.IsDir() {
		_ = walker.visitDirectory(visiblePath, canonicalPath, false)
		return
	}
	walker.visitFile(visiblePath, canonicalPath)
}

func (walker *discoveryWalker) visitFile(visiblePath string, canonicalPath string) {
	if filepath.Ext(canonicalPath) != ".org" {
		return
	}

	visiblePath = filepath.Clean(visiblePath)
	canonicalPath = filepath.Clean(canonicalPath)
	if existing, seen := walker.seenFiles[canonicalPath]; seen {
		if preferVisiblePath(visiblePath, existing.Path) {
			walker.seenFiles[canonicalPath] = File{Path: visiblePath, CanonicalPath: canonicalPath}
		}
		return
	}
	if err := ensureReadableFile(canonicalPath); err != nil {
		walker.addWarning(visiblePath, fmt.Sprintf("open file: %v", err))
		return
	}
	walker.seenFiles[canonicalPath] = File{Path: visiblePath, CanonicalPath: canonicalPath}
}

func (walker *discoveryWalker) addWarning(path string, message string) {
	walker.warnings = append(walker.warnings, Warning{Path: path, Message: message})
}

func ensureReadableFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func preferVisiblePath(candidate string, existing string) bool {
	if len(candidate) != len(existing) {
		return len(candidate) < len(existing)
	}
	return candidate < existing
}

func absolutePath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}
	return filepath.Clean(absolutePath), nil
}

func canonicalizeMissingPath(path string) (string, error) {
	current := path
	unresolvedSuffix := make([]string, 0, 4)

	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolvedCurrent, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", fmt.Errorf("resolve symlinks for %q: %w", current, resolveErr)
			}
			if len(unresolvedSuffix) > 0 {
				resolvedInfo, statErr := os.Stat(resolvedCurrent)
				if statErr != nil {
					return "", fmt.Errorf("stat resolved parent %q: %w", resolvedCurrent, statErr)
				}
				if !resolvedInfo.IsDir() {
					return "", fmt.Errorf("resolve path %q: nearest existing parent %q is not a directory", path, current)
				}
			}
			for index := len(unresolvedSuffix) - 1; index >= 0; index-- {
				resolvedCurrent = filepath.Join(resolvedCurrent, unresolvedSuffix[index])
			}
			return resolvedCurrent, nil
		}
		if !isNotExistError(err) {
			return "", fmt.Errorf("inspect path %q: %w", current, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve path %q: no existing parent directory", path)
		}
		unresolvedSuffix = append(unresolvedSuffix, filepath.Base(current))
		current = parent
	}
}

func isNotExistError(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err))
}
