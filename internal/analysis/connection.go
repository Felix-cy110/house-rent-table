package analysis

import (
	"net/url"
	"strings"
	"unicode"
)

// LoginRequest selects authentication and, for API keys, the configuration source.
// Secrets only travel from the browser to the server, never in response objects.
type LoginRequest struct {
	Type      string `json:"type"`
	APIKey    string `json:"apiKey,omitempty"`
	BaseURL   string `json:"baseUrl,omitempty"`
	Model     string `json:"model,omitempty"`
	Source    string `json:"source,omitempty"`
	ConfigDir string `json:"configDir,omitempty"`
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
	r.ConfigDir = strings.TrimSpace(r.ConfigDir)
	if r.Type == "chatgpt" {
		if r.APIKey != "" || r.BaseURL != "" || r.Model != "" || r.Source != "" || r.ConfigDir != "" {
			return &ConfigError{"OAuth 登录不能附带供应商配置"}
		}
		return nil
	}
	if r.Type != "apiKey" {
		return &ConfigError{"请选择 OAuth 或 API Key 连接"}
	}
	if r.Source == "ccswitch" {
		if r.APIKey != "" || r.BaseURL != "" || r.Model != "" || len(r.ConfigDir) > 4096 || strings.ContainsRune(r.ConfigDir, 0) {
			return &ConfigError{"CC Switch 导入不能混入手动填写的供应商配置"}
		}
		return nil
	}
	if (r.Source != "" && r.Source != "manual") || r.ConfigDir != "" {
		return &ConfigError{"配置来源无效"}
	}
	if r.APIKey == "" || len(r.APIKey) > 8192 || strings.IndexFunc(r.APIKey, unicode.IsControl) >= 0 {
		return &ConfigError{"请填写有效的 API Key"}
	}
	if len(r.Model) > 256 || strings.IndexFunc(r.Model, unicode.IsControl) >= 0 {
		return &ConfigError{"模型名称无效"}
	}
	if r.BaseURL != "" {
		u, err := url.Parse(r.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(r.BaseURL) > 2048 {
			return &ConfigError{"API 地址必须是 HTTP(S) 基础地址，不能包含账号、查询参数或片段"}
		}
		if r.Model == "" {
			return &ConfigError{"自定义 API 地址需要填写供应商支持的模型名称"}
		}
	}
	return nil
}
