package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

func requireCatalogCount(t *testing.T, dir string, want int) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".responses-model-*.json"))
	if err != nil || len(paths) != want {
		t.Fatalf("temporary model catalogs: got %d, want %d; %v", len(paths), want, err)
	}
}

func TestCustomProviderCatalogLifecycle(t *testing.T) {
	s, _ := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.Account(ctx); err != nil {
		t.Fatal(err)
	}
	requireCatalogCount(t, s.workDir, 0)
	input := analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key", BaseURL: "https://gateway.example/v1", Model: "gpt-6-astra"}
	if _, err := s.Login(ctx, input); err != nil {
		t.Fatal(err)
	}
	requireCatalogCount(t, s.workDir, 1)
	t.Setenv("RENT_CODEX_SCENARIO", "init-fail")
	input.Model = "replacement-model"
	if _, err := s.Login(ctx, input); err == nil {
		t.Fatal("failed initialization was accepted")
	}
	requireCatalogCount(t, s.workDir, 1)
	s.Close()
	requireCatalogCount(t, s.workDir, 0)
}

func TestCustomProviderLaunchFailureRemovesCatalog(t *testing.T) {
	dir := t.TempDir()
	brokenBinary := filepath.Join(dir, "invalid.exe")
	if err := os.WriteFile(brokenBinary, []byte("not an executable"), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := start(brokenBinary, dir, dir, &providerConfig{Model: "gpt-6-astra"}, func(message) {}, nil)
	if c != nil {
		c.close()
	}
	if err == nil {
		t.Fatal("invalid executable was launched")
	}
	requireCatalogCount(t, dir, 0)
}
