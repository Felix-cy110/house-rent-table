package analysis

import (
	"net/url"
	"strings"
	"unicode"
)

// LoginRequest configures a direct, OpenAI-compatible HTTP API connection.
// Secrets only travel from the browser to the server, never in response objects.
type LoginRequest struct {
	APIKey  string `json:"apiKey,omitempty"`
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
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
	r.BaseURL = strings.TrimRight(strings.TrimSpace(r.BaseURL), "/")
	r.Model = strings.TrimSpace(r.Model)
	if r.APIKey == "" || len(r.APIKey) > 8192 || strings.IndexFunc(r.APIKey, unicode.IsControl) >= 0 {
		return &ConfigError{"请填写有效的 API Key"}
	}
	if r.Model == "" || len(r.Model) > 256 || strings.IndexFunc(r.Model, unicode.IsControl) >= 0 {
		return &ConfigError{"请填写有效的模型名称"}
	}
	if r.BaseURL == "" {
		return &ConfigError{"请填写 API URL"}
	}
	u, err := url.Parse(r.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(r.BaseURL) > 2048 {
		return &ConfigError{"API 地址必须是 HTTP(S) 基础地址，不能包含账号、查询参数或片段"}
	}
	return nil
}
