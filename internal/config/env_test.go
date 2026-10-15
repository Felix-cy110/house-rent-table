package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func unset(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // Restore the original process environment during cleanup.
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEnvFilePreservesEnvironmentAndQuotedSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	unset(t, "RENT_MYSQL_DSN")
	t.Setenv("RENT_DEFAULT_CURRENCY", "USD")
	t.Setenv("RENT_PERSISTENCE_DIR", "")
	fixture := "\ufeff# local configuration\r\nexport RENT_MYSQL_DSN='example:fake$pass#word@tcp(127.0.0.1:3306)/test'\r\nRENT_DEFAULT_CURRENCY=CNY\r\nRENT_PERSISTENCE_DIR=ignored\r\n"
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("RENT_MYSQL_DSN") != "example:fake$pass#word@tcp(127.0.0.1:3306)/test" {
		t.Fatal("quoted DSN changed")
	}
	if os.Getenv("RENT_DEFAULT_CURRENCY") != "USD" || os.Getenv("RENT_PERSISTENCE_DIR") != "" {
		t.Fatal("existing environment was overwritten")
	}
	// Explicitly empty DSN keeps browser tests and deployments disabled.
	t.Setenv("RENT_MYSQL_DSN", "")
	if err := LoadEnvFile(path); err != nil || os.Getenv("RENT_MYSQL_DSN") != "" {
		t.Fatal("empty DSN override was ignored")
	}
}

func TestLoadEnvFileErrorsDoNotExposeSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	unset(t, "RENT_MYSQL_DSN")
	if err := LoadEnvFile(path); err != nil {
		t.Fatal("missing optional file should be allowed")
	}
	const secret = "fake-secret-do-not-print"
	if err := os.WriteFile(path, []byte("RENT_MYSQL_DSN='"+secret), 0600); err != nil {
		t.Fatal(err)
	}
	err := LoadEnvFile(path)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("invalid dotenv must fail without exposing its content")
	}
	if _, exists := os.LookupEnv("RENT_MYSQL_DSN"); exists {
		t.Fatal("invalid dotenv changed the environment")
	}
	if err = LoadEnvFile(t.TempDir()); err == nil {
		t.Fatal("unreadable dotenv must fail")
	}
}
