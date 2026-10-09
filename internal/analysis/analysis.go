package analysis

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrLoginRequired = errors.New("codex login required")
	ErrUnavailable   = errors.New("codex unavailable")
	ErrBusy          = errors.New("codex busy")
	ErrFailed        = errors.New("codex analysis failed")
)

// Analyzer receives the original, validated JSON bytes and returns final text.
// Spreadsheet cells are untrusted facts; implementations must not treat them
// as agent instructions. The original document must remain unchanged.
type Analyzer interface {
	Analyze(context.Context, json.RawMessage) (Result, error)
}

type Result struct {
	Text string `json:"text"`
}

type Account struct {
	LoggedIn bool   `json:"loggedIn"`
	AuthType string `json:"authType,omitempty"`
	Email    string `json:"email,omitempty"`
	Pending  bool   `json:"pending"`
	Error    string `json:"error,omitempty"`
}

type Login struct {
	Type    string `json:"type"`
	AuthURL string `json:"authUrl,omitempty"`
}

type Authenticator interface {
	Account(context.Context) (Account, error)
	Login(context.Context, string, string) (Login, error)
	Logout(context.Context) error
}

type Unconfigured struct{}

func (Unconfigured) Analyze(context.Context, json.RawMessage) (Result, error) {
	return Result{}, ErrUnavailable
}
