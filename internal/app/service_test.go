package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewServiceUsesDefaultConfigPath(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(homeDir, "xdg-data"))

	configPath := filepath.Join(homeDir, "xdg-config", "org-search", "config.txtpb")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`notes_root: "/notes"`), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	_, err := NewService().Rebuild(context.Background(), RebuildRequest{})
	if err == nil || !strings.Contains(err.Error(), "rebuild is not implemented yet") {
		t.Fatalf("expected unimplemented rebuild error after loading default config, got %v", err)
	}
}

func TestNewServiceReturnsConfigErrorsBeforeOperationErrors(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, "xdg-config"))

	_, err := NewService().Search(context.Background(), SearchRequest{})
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("expected config read error, got %v", err)
	}
}
