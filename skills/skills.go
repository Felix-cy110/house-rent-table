// Package skills bundles the application's review instructions with the binary.
package skills

import (
	_ "embed"
	"os"
	"path/filepath"
)

const Name = "rental-review"

//go:embed rental-review/SKILL.md
var rentalReview []byte

// Install publishes the bundled skill inside the app's isolated workspace so
// Codex can both discover it and accept its absolute path as a skill input.
func Install(workspace string) (string, error) {
	path := filepath.Join(workspace, ".agents", "skills", Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, rentalReview, 0600); err != nil {
		return "", err
	}
	return path, nil
}
