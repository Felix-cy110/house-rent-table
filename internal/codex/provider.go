package codex

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/pelletier/go-toml/v2"
)

const providerFile = "rental-provider.json"
const providerKeyEnv = "RENT_CODEX_PROVIDER_API_KEY"

// This private snapshot is independent of CC Switch's live files. Never return it
// through the HTTP API or pass its secret in command-line arguments.
type providerConfig struct {
	originDir string
	Name      string `json:"name"`
	Source    string `json:"source"`
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	APIKey    string `json:"apiKey"`
}

func (p *providerConfig) validate() error {
	r := analysis.LoginRequest{Type: "apiKey", APIKey: p.APIKey, BaseURL: p.BaseURL, Model: p.Model}
	if err := r.Validate(); err != nil {
		return err
	}
	p.APIKey, p.BaseURL, p.Model = r.APIKey, r.BaseURL, r.Model
	if p.BaseURL == "" {
		return &analysis.ConfigError{Message: "供应商缺少 API 地址，请重新连接"}
	}
	if p.Source != "manual" && p.Source != "ccswitch" {
		return &analysis.ConfigError{Message: "供应商配置来源无效，请重新连接"}
	}
	return nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("configuration unreadable or too large")
	}
	return data, nil
}

func loadProvider(home string) (*providerConfig, error) {
	data, err := readLimited(filepath.Join(home, providerFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var p *providerConfig
	if err != nil || json.Unmarshal(data, &p) != nil {
		return nil, &analysis.ConfigError{Message: "本应用的供应商配置无法读取，请重新连接"}
	}
	if p != nil {
		if err := p.validate(); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func saveProvider(home string, p *providerConfig) error {
	data, err := json.Marshal(p)
	if err != nil {
		return analysis.ErrUnavailable
	}
	f, err := os.CreateTemp(home, ".provider-*")
	if err != nil {
		return analysis.ErrUnavailable
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return analysis.ErrUnavailable
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return analysis.ErrUnavailable
	}
	if err = f.Close(); err != nil {
		return analysis.ErrUnavailable
	}
	if err = os.Rename(tmp, filepath.Join(home, providerFile)); err != nil {
		return analysis.ErrUnavailable
	}
	return nil
}

type codexProvider struct {
	Name               string            `toml:"name"`
	BaseURL            string            `toml:"base_url"`
	WireAPI            string            `toml:"wire_api"`
	EnvKey             string            `toml:"env_key"`
	Token              string            `toml:"experimental_bearer_token"`
	RequiresOpenAIAuth bool              `toml:"requires_openai_auth"`
	HTTPHeaders        map[string]string `toml:"http_headers"`
	EnvHTTPHeaders     map[string]string `toml:"env_http_headers"`
	QueryParams        map[string]string `toml:"query_params"`
}

type codexConfig struct {
	Model    string `toml:"model"`
	Provider string `toml:"model_provider"`
	BaseURL  string `toml:"base_url"`
	Token    string `toml:"experimental_bearer_token"`
	Profile  string `toml:"profile"`
	Profiles map[string]struct {
		Model    string `toml:"model"`
		Provider string `toml:"model_provider"`
	} `toml:"profiles"`
	Providers map[string]codexProvider `toml:"model_providers"`
}

func expandConfigDir(dir, userHome string) (string, error) {
	if dir == "~" {
		dir = userHome
	} else if strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		dir = filepath.Join(userHome, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		return "", &analysis.ConfigError{Message: "请填写 CC Switch 使用的 Codex 配置目录的绝对路径"}
	}
	return filepath.Clean(dir), nil
}

func importDir(explicit, userHome string) (string, error) {
	if explicit != "" {
		return expandConfigDir(explicit, userHome)
	}
	// Older releases store this device-specific override in settings.json.
	// The optional directory field also supports newer database-backed settings.
	data, err := readLimited(filepath.Join(userHome, ".cc-switch", "settings.json"))
	if err == nil {
		var settings struct {
			CodexConfigDir string `json:"codexConfigDir"`
		}
		if json.Unmarshal(data, &settings) != nil {
			return "", &analysis.ConfigError{Message: "CC Switch 设置无法读取，请填写 Codex 配置目录后重试"}
		}
		if dir := strings.TrimSpace(settings.CodexConfigDir); dir != "" {
			return expandConfigDir(dir, userHome)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", &analysis.ConfigError{Message: "CC Switch 设置无法读取，请填写 Codex 配置目录后重试"}
	}
	return filepath.Join(userHome, ".codex"), nil
}

func importCCSwitch(dir, userHome string) (*providerConfig, error) {
	dir, err := importDir(dir, userHome)
	if err != nil {
		return nil, err
	}
	data, err := readLimited(filepath.Join(dir, "config.toml"))
	if err != nil {
		return nil, &analysis.ConfigError{Message: "未找到可读取的 Codex 配置，请先在 CC Switch 启用供应商，或填写其 Codex 配置目录"}
	}
	var cfg codexConfig
	if toml.Unmarshal(data, &cfg) != nil {
		return nil, &analysis.ConfigError{Message: "Codex 配置格式无效，请在 CC Switch 中检查后重新导入"}
	}
	if cfg.Profile != "" {
		profile, ok := cfg.Profiles[cfg.Profile]
		if !ok {
			return nil, &analysis.ConfigError{Message: "Codex 当前配置档案不存在，请在 CC Switch 中检查配置"}
		}
		if profile.Model != "" {
			cfg.Model = profile.Model
		}
		if profile.Provider != "" {
			cfg.Provider = profile.Provider
		}
	}
	active, ok := cfg.Providers[cfg.Provider]
	if cfg.Provider != "" && cfg.Provider != "openai" && !ok {
		return nil, &analysis.ConfigError{Message: "未找到 Codex 当前启用的供应商配置"}
	}
	if active.BaseURL == "" {
		active.BaseURL = cfg.BaseURL
	}
	if active.BaseURL == "" && (cfg.Provider == "" || cfg.Provider == "openai") {
		active.BaseURL = "https://api.openai.com/v1"
	}
	if active.BaseURL == "" || cfg.Model == "" {
		return nil, &analysis.ConfigError{Message: "当前供应商缺少 API 地址或模型，请在 CC Switch 中补全后重新导入"}
	}
	if active.WireAPI != "" && active.WireAPI != "responses" {
		return nil, &analysis.ConfigError{Message: "当前接口不是 Responses 格式，请先在 CC Switch 开启兼容路由后重新导入"}
	}
	if len(active.HTTPHeaders) > 0 || len(active.EnvHTTPHeaders) > 0 || len(active.QueryParams) > 0 {
		return nil, &analysis.ConfigError{Message: "当前配置含额外请求头或参数，请通过 CC Switch 的本地路由连接后重新导入"}
	}
	p := &providerConfig{originDir: dir, Name: active.Name, Source: "ccswitch", BaseURL: active.BaseURL, Model: cfg.Model}
	if p.Name == "" {
		p.Name = cfg.Provider
	}
	if p.Name == "" {
		p.Name = "OpenAI"
	}
	if !active.RequiresOpenAIAuth {
		if active.EnvKey != "" {
			p.APIKey = os.Getenv(active.EnvKey)
			if strings.TrimSpace(p.APIKey) == "" {
				return nil, &analysis.ConfigError{Message: "供应商使用的密钥环境变量在本服务中未设置，请设置后重启服务，或手动填写 API Key"}
			}
		} else {
			p.APIKey = active.Token
			if p.APIKey == "" && (cfg.Provider == "" || cfg.Provider == "openai") {
				p.APIKey = cfg.Token
			}
		}
	}
	if p.APIKey == "" {
		data, err := readLimited(filepath.Join(dir, "auth.json"))
		var auth struct {
			Key  string `json:"OPENAI_API_KEY"`
			Mode string `json:"auth_mode"`
		}
		if err != nil || json.Unmarshal(data, &auth) != nil || (auth.Mode != "" && auth.Mode != "apikey") || strings.TrimSpace(auth.Key) == "" {
			return nil, &analysis.ConfigError{Message: "当前配置没有可导入的 API Key；请在 CC Switch 选择 API 供应商，ChatGPT 账号请使用 OAuth 登录"}
		}
		p.APIKey = auth.Key
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}
