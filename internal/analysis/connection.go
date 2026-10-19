package analysis

import (
	"strings"
	"unicode"

	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
)

// LoginRequest selects one fixed provider; URL and model overrides are rejected.
// Secrets only travel from the browser to the server, never in response objects.
type LoginRequest struct {
	APIKey   string `json:"apiKey,omitempty"`
	Provider string `json:"provider"`
}

// ConfigError contains a safe, locally authored message, never a parser or upstream error.
type ConfigError struct{ Message string }

func (e *ConfigError) Error() string { return e.Message }

// ConnectionError reports a failed model probe using only locally authored text.
type ConnectionError struct {
	Message string
	Cause   error
}

func (e *ConnectionError) Error() string { return e.Message }
func (e *ConnectionError) Unwrap() error { return e.Cause }

func (r *LoginRequest) Validate() error {
	r.APIKey = strings.TrimSpace(r.APIKey)
	r.Provider = strings.TrimSpace(r.Provider)
	if r.APIKey == "" || len(r.APIKey) > 8192 || strings.IndexFunc(r.APIKey, unicode.IsControl) >= 0 {
		return &ConfigError{"请填写有效的 API Key"}
	}
	if _, ok := modelconfig.Lookup(r.Provider); !ok {
		return &ConfigError{"请选择 MiMo、GLM 或 DeepSeek 的受支持模型"}
	}
	return nil
}
