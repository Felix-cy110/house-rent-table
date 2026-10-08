package analysis

import (
	"context"
	"errors"

	"github.com/Felix-cy110/house-rent-table/internal/document"
)

var ErrNotConfigured = errors.New("analysis not configured")

// Analyzer is the only integration point for the future agent and its skill.
// Spreadsheet cells are untrusted facts; implementations must not treat them
// as agent instructions. The original document must remain unchanged.
type Analyzer interface {
	Analyze(context.Context, document.Document) (Result, error)
}

type Finding struct {
	ID               string   `json:"id"`
	Severity         string   `json:"severity"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	EvidenceFieldIDs []string `json:"evidenceFieldIds"`
	FollowUp         string   `json:"followUp"`
}

type MissingInformation struct {
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

type Result struct {
	Summary            string               `json:"summary"`
	Findings           []Finding            `json:"findings"`
	MissingInformation []MissingInformation `json:"missingInformation"`
}

type Unconfigured struct{}

func (Unconfigured) Analyze(context.Context, document.Document) (Result, error) {
	return Result{}, ErrNotConfigured
}
