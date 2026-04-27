// Package discovery resolves reachable Org files into canonical absolute file identities.
package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Result stores the canonical Org file paths and non-fatal warnings from one discovery pass.
type Result struct {
	Paths    []string
	Warnings []Warning
}

// Warning describes one non-fatal discovery problem for a reachable path.
type Warning struct {
	Path    string
	Message string
}

// Discover walks one notes root, follows reachable symlinks, and returns deduplicated canonical Org file paths.
func Discover(root string) (Result, error) {
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
		seenFiles:       map[string]struct{}{},
	}
	if err := walker.visitDirectory(canonicalRoot, true); err != nil {
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
	seenFiles       map[string]struct{}
	warnings        []Warning
}

func (walker *discoveryWalker) result() Result {
	paths := make([]string, 0, len(walker.seenFiles))
	for path := range walker.seenFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	warnings := append([]Warning(nil), walker.warnings...)
	sort.Slice(warnings, func(left int, right int) bool {
		if warnings[left].Path == warnings[right].Path {
			return warnings[left].Message < warnings[right].Message
		}
		return warnings[left].Path < warnings[right].Path
	})

	return Result{Paths: paths, Warnings: warnings}
}

func (walker *discoveryWalker) visitDirectory(path string, isRoot bool) error {
	if _, seen := walker.seenDirectories[path]; seen {
		return nil
	}
	walker.seenDirectories[path] = struct{}{}

	entries, err := os.ReadDir(path)
	if err != nil {
		if isRoot {
			return fmt.Errorf("read notes root %q: %w", path, err)
		}
		walker.addWarning(path, fmt.Sprintf("read directory: %v", err))
		return nil
	}

	for _, entry := range entries {
		walker.visitPath(filepath.Join(path, entry.Name()))
	}
	return nil
}

func (walker *discoveryWalker) visitPath(path string) {
	info, err := os.Lstat(path)
	if err != nil {
		walker.addWarning(path, fmt.Sprintf("inspect path: %v", err))
		return
	}

	if info.Mode()&os.ModeSymlink != 0 {
		walker.visitSymlink(path)
		return
	}
	if info.IsDir() {
		_ = walker.visitDirectory(path, false)
		return
	}
	walker.visitFile(path)
}

func (walker *discoveryWalker) visitSymlink(path string) {
	canonicalPath, err := CanonicalizePath(path)
	if err != nil {
		walker.addWarning(path, err.Error())
		return
	}

	info, err := os.Stat(canonicalPath)
	if err != nil {
		walker.addWarning(path, fmt.Sprintf("stat resolved path %q: %v", canonicalPath, err))
		return
	}
	if info.IsDir() {
		_ = walker.visitDirectory(canonicalPath, false)
		return
	}
	walker.visitFile(canonicalPath)
}

func (walker *discoveryWalker) visitFile(path string) {
	if filepath.Ext(path) != ".org" {
		return
	}

	canonicalPath, err := CanonicalizePath(path)
	if err != nil {
		walker.addWarning(path, err.Error())
		return
	}
	if _, seen := walker.seenFiles[canonicalPath]; seen {
		return
	}
	if err := ensureReadableFile(canonicalPath); err != nil {
		walker.addWarning(canonicalPath, fmt.Sprintf("open file: %v", err))
		return
	}
	walker.seenFiles[canonicalPath] = struct{}{}
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
