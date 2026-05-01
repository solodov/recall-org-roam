package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReadsConfigFile(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(homeDir, ".local", "share"))

	configPath := filepath.Join(t.TempDir(), "config.txtpb")
	raw := `notes_root: "~/org"`
	if err := osWriteFile(configPath, []byte(raw)); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.NotesRoot, filepath.Join(homeDir, "org"); got != want {
		t.Fatalf("notes_root = %q, want %q", got, want)
	}
}

func TestLoadBytesDefaultsIndexDirectoryFromXDGDataHome(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	xdgDataHome := filepath.Join(homeDir, "xdg-data")
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_DATA_HOME", xdgDataHome)

	cfg, err := LoadBytes("test.txtpb", []byte(`notes_root: "/notes"`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.IndexDirectory, filepath.Join(xdgDataHome, "recall-org-roam", defaultIndexDirectoryName); got != want {
		t.Fatalf("index_directory = %q, want %q", got, want)
	}
}

func TestLoadBytesFallsBackToLocalShareDefaultIndexDirectory(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_DATA_HOME", "")

	cfg, err := LoadBytes("test.txtpb", []byte(`notes_root: "/notes"`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.IndexDirectory, filepath.Join(homeDir, ".local", "share", "recall-org-roam", defaultIndexDirectoryName); got != want {
		t.Fatalf("index_directory = %q, want %q", got, want)
	}
}

func TestLoadBytesNormalizesHomeRelativePaths(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(homeDir, "xdg-data"))

	cfg, err := LoadBytes("test.txtpb", []byte(`
notes_root: "  ~/notes  "
index_directory: " ~/index "
excluded_directory_names: " excluded-one/ "
excluded_directory_names: "excluded-two"
excluded_directory_names: "excluded-three//"
excluded_directory_names: "excluded-two/"
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.NotesRoot, filepath.Join(homeDir, "notes"); got != want {
		t.Fatalf("notes_root = %q, want %q", got, want)
	}
	if got, want := cfg.IndexDirectory, filepath.Join(homeDir, "index"); got != want {
		t.Fatalf("index_directory = %q, want %q", got, want)
	}
	if got, want := strings.Join(cfg.ExcludedDirectoryNames, ","), "excluded-one,excluded-two,excluded-three"; got != want {
		t.Fatalf("excluded_directory_names = %q, want %q", got, want)
	}
}

func TestLoadBytesRequiresNotesRoot(t *testing.T) {
	t.Helper()

	_, err := LoadBytes("test.txtpb", []byte(`index_directory: "/tmp/index"`))
	if err == nil || !strings.Contains(err.Error(), "notes_root is required") {
		t.Fatalf("expected missing notes_root error, got %v", err)
	}
}

func TestLoadBytesRejectsRelativeNotesRoot(t *testing.T) {
	t.Helper()

	_, err := LoadBytes("test.txtpb", []byte(`notes_root: "notes"`))
	if err == nil || !strings.Contains(err.Error(), "must be absolute after normalization") {
		t.Fatalf("expected relative notes_root error, got %v", err)
	}
}

func TestLoadBytesRejectsRelativeIndexDirectory(t *testing.T) {
	t.Helper()

	_, err := LoadBytes("test.txtpb", []byte(`notes_root: "/notes" index_directory: "index"`))
	if err == nil || !strings.Contains(err.Error(), "index_directory: must be absolute after normalization") {
		t.Fatalf("expected relative index_directory error, got %v", err)
	}
}

func TestLoadBytesRejectsExcludedDirectoryNamePaths(t *testing.T) {
	t.Helper()

	_, err := LoadBytes("test.txtpb", []byte(`notes_root: "/notes" excluded_directory_names: "excluded/nested"`))
	if err == nil || !strings.Contains(err.Error(), "excluded_directory_names") {
		t.Fatalf("expected excluded_directory_names validation error, got %v", err)
	}
}

func TestLoadBytesRejectsRelativeXDGDataHomeForDefaultIndexDirectory(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_DATA_HOME", "relative/data")

	_, err := LoadBytes("test.txtpb", []byte(`notes_root: "/notes"`))
	if err == nil || !strings.Contains(err.Error(), "XDG_DATA_HOME") {
		t.Fatalf("expected relative XDG_DATA_HOME error, got %v", err)
	}
}

func TestResolvePathDefaultsToXDGConfigHome(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	xdgConfigHome := filepath.Join(homeDir, "xdg-config")
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	resolvedPath, err := ResolvePath("")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}

	if got, want := resolvedPath, filepath.Join(xdgConfigHome, "recall-org-roam", defaultConfigFileName); got != want {
		t.Fatalf("configPath = %q, want %q", got, want)
	}
}

func TestResolvePathFallsBackToDotConfig(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", "")

	resolvedPath, err := ResolvePath("")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}

	if got, want := resolvedPath, filepath.Join(homeDir, ".config", "recall-org-roam", defaultConfigFileName); got != want {
		t.Fatalf("configPath = %q, want %q", got, want)
	}
}

func TestResolvePathRejectsRelativeOverride(t *testing.T) {
	t.Helper()

	_, err := ResolvePath("config.txtpb")
	if err == nil || !strings.Contains(err.Error(), "must be absolute after normalization") {
		t.Fatalf("expected relative config path error, got %v", err)
	}
}

func osWriteFile(path string, raw []byte) error {
	return os.WriteFile(path, raw, 0o600)
}
