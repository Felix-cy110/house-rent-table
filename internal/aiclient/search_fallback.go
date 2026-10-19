package aiclient

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
)

// SearchFallback is server-owned and never persisted with the user's model key.
// Its zero value disables fallback; construct other values with NewSearchFallback.
type SearchFallback struct {
	provider modelconfig.Provider
	key      string
}

func NewSearchFallback(provider, key string) (SearchFallback, error) {
	provider = strings.TrimSpace(provider)
	if len(key) > 8192 || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return SearchFallback{}, errors.New("RENT_SEARCH_API_KEY 格式无效")
	}
	key = strings.TrimSpace(key)
	if provider == "" && key == "" {
		return SearchFallback{}, nil
	}
	if key == "" {
		return SearchFallback{}, errors.New("设置 RENT_SEARCH_PROVIDER 时必须同时设置 RENT_SEARCH_API_KEY")
	}
	if provider == "" {
		provider = "glm"
	}
	if provider != "glm" && provider != "deepseek" {
		return SearchFallback{}, errors.New("RENT_SEARCH_PROVIDER 只支持 glm 或 deepseek")
	}
	p, _ := modelconfig.Lookup(provider)
	return SearchFallback{provider: p, key: key}, nil
}

// MiMo documents no dedicated inactive-plugin code. Match only explicit search
// activation failures from error fields, never generated content or HTTP 403 alone.
func searchPluginInactive(message string) bool {
	if len(message) > 4096 {
		return false
	}
	message = strings.ToLower(message)
	message = strings.NewReplacer("_", " ", "-", " ").Replace(message)
	message = strings.Join(strings.Fields(message), " ")
	search := strings.Contains(message, "web search") || strings.Contains(message, "websearch") || strings.Contains(message, "联网搜索") || strings.Contains(message, "搜索插件")
	if !search {
		return false
	}
	for _, state := range []string{"not activated", "not enabled", "not been activated", "not been enabled", "must be activated", "must be enabled", "please activate", "please enable", "未开通", "未启用", "未激活", "未开启", "请先开通"} {
		if strings.Contains(message, state) {
			return true
		}
	}
	return false
}

func inactiveSearchError(raw json.RawMessage) bool {
	if message := optionalString(raw); message != "" {
		return searchPluginInactive(message)
	}
	return searchPluginInactive(optionalString(optionalObject(raw)["message"]))
}

func inactiveSearchEnvelope(raw json.RawMessage) bool {
	body := optionalObject(raw)
	if hasError(body["error"]) {
		return inactiveSearchError(body["error"])
	}
	return searchPluginInactive(optionalString(body["message"]))
}
