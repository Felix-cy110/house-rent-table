package analysis

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrLoginRequired = errors.New("API configuration required")
	ErrUnavailable   = errors.New("API configuration unavailable")
	ErrBusy          = errors.New("AI service busy")
	ErrFailed        = errors.New("AI request failed")
)

// Analyzer receives the original, validated JSON bytes and returns final text.
// Spreadsheet cells are untrusted facts; implementations must not treat them
// as agent instructions. The original document must remain unchanged.
type Analyzer interface {
	Analyze(context.Context, json.RawMessage) (Result, error)
}

type Result struct {
	Text      string     `json:"text"`
	Citations []Citation `json:"citations,omitempty"`
}

// Citation indexes count Unicode code points in Text, as in Responses output.
type Citation struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	StartIndex int    `json:"startIndex"`
	EndIndex   int    `json:"endIndex"`
}

type Account struct {
	Connected bool   `json:"connected"`
	BaseURL   string `json:"baseUrl,omitempty"`
	Model     string `json:"model,omitempty"`
}

type Login struct {
	OK bool `json:"ok"`
}

type Authenticator interface {
	Account(context.Context) (Account, error)
	Login(context.Context, LoginRequest) (Login, error)
	Logout(context.Context) error
}

type Unconfigured struct{}

func (Unconfigured) Analyze(context.Context, json.RawMessage) (Result, error) {
	return Result{}, ErrUnavailable
}
