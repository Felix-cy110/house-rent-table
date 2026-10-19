// Package modelconfig is the single catalog of supported model connections.
package modelconfig

// Provider contains public display metadata and fixed, server-only endpoints.
// VersionNote is a dated observation of an upstream alias, not a version pin.
type Provider struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Model            string `json:"model"`
	VersionNote      string `json:"versionNote,omitempty"`
	SearchNote       string `json:"searchNote"`
	Thinking         bool   `json:"thinking"`
	Endpoint         string `json:"-"`
	SearchEndpoint   string `json:"-"`
	SearchQueryLimit int    `json:"-"`
}

func All() []Provider {
	return []Provider{
		{ID: "mimo", Name: "MiMo v2.6-Flash", Model: "mimo-v2.6-flash", Thinking: true, Endpoint: "https://api.xiaomimimo.com/v1/chat/completions", SearchNote: "联网核价需在小米开放平台开通 Web Search 插件；搜索按供应商规则计费。"},
		{ID: "glm", Name: "GLM 5.3-Flash", Model: "glm-5.3-flash", Thinking: true, Endpoint: "https://open.bigmodel.cn/api/paas/v4/chat/completions", SearchEndpoint: "https://open.bigmodel.cn/api/paas/v4/web_search", SearchQueryLimit: 70, SearchNote: "联网核价使用同一 Key 调用智谱 Web Search API，搜索单独计费；使用标准 API Key。"},
		{ID: "deepseek", Name: "DeepSeek Flash", Model: "deepseek-flash", Thinking: true, Endpoint: "https://api.deepseek.com/chat/completions", SearchEndpoint: "https://api.deepseek.com/v1/tools/search", SearchQueryLimit: 128, VersionNote: "截至 2026-10-19 对应 DeepSeek V4.1-Flash-1017；官方别名可能随版本更新。", SearchNote: "联网核价使用同一 Key 调用 DeepSeek Search API，搜索单独计费。"},
	}
}

func Lookup(id string) (Provider, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}
