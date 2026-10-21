package aiclient

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
)

type Finish string

const (
	Finished   Finish = "completed"
	NeedsTools Finish = "tool_calls"
	Incomplete Finish = "incomplete"
	Refused    Finish = "refused"
	Failed     Finish = "failed"
)

// NormalizedResponse is the only model response read by orchestration and
// business services. Wire fields and reasoning are also retained in turn logs.
type NormalizedResponse struct {
	Provider                string
	Model                   string
	Text                    string
	Finish                  Finish
	ToolCalls               []ToolCall
	Sources                 []analysis.Source
	Citations               []analysis.Citation
	Warnings                []string
	Usage                   *Usage
	HostedSearchUnavailable bool
}

type ToolCall struct{ ID, Name, Arguments string }

// Nil means unreported, not zero. Counters represent tokens, not prices.
type Usage struct {
	InputTokens         *int64 `json:"inputTokens"`
	OutputTokens        *int64 `json:"outputTokens"`
	TotalTokens         *int64 `json:"totalTokens"`
	ReasoningTokens     *int64 `json:"reasoningTokens"`
	CachedInputTokens   *int64 `json:"cachedInputTokens"`
	UncachedInputTokens *int64 `json:"uncachedInputTokens"`
	SearchCalls         *int64 `json:"searchCalls"`
	SearchPages         *int64 `json:"searchPages"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Only documented continuation fields are sent back. Unknown response-only
// extensions are ignored; optional reasoning retains absence, null and content.
type chatMessage struct {
	Role       string          `json:"role"`
	Content    *string         `json:"content"`
	Reasoning  json.RawMessage `json:"reasoning_content,omitempty"`
	ToolCalls  []toolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type object map[string]json.RawMessage

func optionalObject(raw json.RawMessage) object { var v object; _ = json.Unmarshal(raw, &v); return v }
func optionalString(raw json.RawMessage) string { var v string; _ = json.Unmarshal(raw, &v); return v }
func optionalArray(raw json.RawMessage) []json.RawMessage {
	var v []json.RawMessage
	_ = json.Unmarshal(raw, &v)
	return v
}
func optionalInt(raw json.RawMessage) *int64 {
	if !hasError(raw) {
		return nil
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil || bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		return nil
	}
	v, err := n.Int64()
	if err != nil {
		// GLM documents some counters as JSON numbers; accept integral 12.0
		// and 1e3 without rounding fractional counts or overflowing int64.
		digits := n.String()
		if len(digits) > 64 {
			return nil
		}
		if i := strings.IndexAny(digits, "eE"); i >= 0 && len(digits[i+1:]) > 4 {
			return nil
		}
		rat, ok := new(big.Rat).SetString(digits)
		if !ok || !rat.IsInt() || !rat.Num().IsInt64() {
			return nil
		}
		v = rat.Num().Int64()
	}
	if v < 0 {
		return nil
	}
	return &v
}
func hasError(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func normalize(p modelconfig.Provider, raw json.RawMessage) (NormalizedResponse, chatMessage, error) {
	n := NormalizedResponse{Provider: p.ID, Model: p.Model, Finish: Failed}
	var root struct {
		Model     json.RawMessage `json:"model"`
		Error     json.RawMessage `json:"error"`
		Usage     json.RawMessage `json:"usage"`
		WebSearch json.RawMessage `json:"web_search"`
		Choices   []struct {
			FinishReason string `json:"finish_reason"`
			Message      object `json:"message"`
		} `json:"choices"`
	}
	var message chatMessage
	if json.Unmarshal(raw, &root) != nil {
		return n, message, analysis.ErrFailed
	}
	if hasError(root.Error) {
		if p.ID == "mimo" && len(root.Choices) == 0 && inactiveSearchError(root.Error) {
			n.HostedSearchUnavailable = true
			n.Usage = normalizeUsage(p.ID, root.Usage)
			return n, message, nil
		}
		return n, message, analysis.ErrFailed
	}
	if len(root.Choices) != 1 {
		return n, message, analysis.ErrFailed
	}
	choice := root.Choices[0]
	m := choice.Message
	if m == nil {
		return n, message, analysis.ErrFailed
	}
	if model := optionalString(root.Model); strings.TrimSpace(model) != "" {
		n.Model = model
	}
	message.Role = "assistant"
	if role := optionalString(m["role"]); role != "" && role != "assistant" {
		return n, message, analysis.ErrFailed
	}
	if json.Unmarshal(m["content"], &message.Content) != nil && len(m["content"]) != 0 {
		return n, message, analysis.ErrFailed
	}
	if message.Content != nil {
		n.Text = *message.Content
	}
	if raw := m["reasoning_content"]; len(raw) != 0 {
		var reasoning *string
		// Malformed reasoning cannot safely be replayed into the next tool round.
		if json.Unmarshal(raw, &reasoning) != nil {
			return n, message, analysis.ErrFailed
		}
		message.Reasoning = raw
	}
	if hasError(m["tool_calls"]) && json.Unmarshal(m["tool_calls"], &message.ToolCalls) != nil {
		return n, message, analysis.ErrFailed
	}
	seen := map[string]bool{}
	for _, call := range message.ToolCalls {
		if call.Type != "function" || strings.TrimSpace(call.ID) == "" || seen[call.ID] || strings.TrimSpace(call.Function.Name) == "" || len(n.ToolCalls) >= 64 {
			return n, message, analysis.ErrFailed
		}
		seen[call.ID] = true
		n.ToolCalls = append(n.ToolCalls, ToolCall{call.ID, call.Function.Name, call.Function.Arguments})
	}
	switch choice.FinishReason {
	case "stop":
		n.Finish = Finished
	case "tool_calls":
		n.Finish = NeedsTools
	case "length", "model_context_window_exceeded", "repetition_truncation":
		n.Finish = Incomplete
	case "content_filter", "sensitive":
		n.Finish = Refused
	}
	if hasError(m["refusal"]) && optionalString(m["refusal"]) != "" {
		n.Finish = Refused
	}
	if hasError(m["refusal"]) && optionalString(m["refusal"]) == "" && string(m["refusal"]) != `""` {
		n.Finish = Refused
	}
	if n.Finish == Finished && len(n.ToolCalls) != 0 || n.Finish == NeedsTools && len(n.ToolCalls) == 0 {
		return n, message, analysis.ErrFailed
	}
	if searchError := optionalString(m["error_message"]); searchError != "" {
		n.HostedSearchUnavailable = p.ID == "mimo" && (n.Finish == Finished || n.Finish == NeedsTools) && searchPluginInactive(searchError)
		if !n.HostedSearchUnavailable {
			n.Warnings = append(n.Warnings, "联网搜索未完整完成，相关市价仍需核实。")
		}
	}
	if n.Finish == Finished && strings.TrimSpace(n.Text) == "" {
		if !n.HostedSearchUnavailable {
			return n, message, analysis.ErrFailed
		}
		// A plugin error can arrive without a generated answer. Treat it as a
		// failed search attempt eligible for fallback, never a completed report.
		n.Finish = Failed
	}
	for _, item := range optionalArray(m["annotations"]) {
		a := optionalObject(item)
		if optionalString(a["type"]) != "url_citation" {
			continue
		}
		if nested := optionalObject(a["url_citation"]); nested != nil {
			a = nested
		}
		source := analysis.Source{URL: optionalString(a["url"]), Title: optionalString(a["title"]), Site: optionalString(a["site_name"]), PublishedAt: optionalString(a["publish_time"])}
		n.Sources = mergeSources(n.Sources, []analysis.Source{source})
		start, end := optionalInt(a["start_index"]), optionalInt(a["end_index"])
		if safeURL(source.URL) && start != nil && end != nil && *end > *start && *end <= int64(utf8.RuneCountInString(n.Text)) {
			n.Citations = append(n.Citations, analysis.Citation{Title: source.Title, URL: source.URL, StartIndex: int(*start), EndIndex: int(*end)})
		}
	}
	// GLM puts sources at the top level, while MiMo uses message.annotations.
	if p.ID == "glm" {
		for _, raw := range optionalArray(root.WebSearch) {
			source := optionalObject(raw)
			n.Sources = mergeSources(n.Sources, []analysis.Source{{Title: optionalString(source["title"]), URL: optionalString(source["link"]), Site: optionalString(source["media"]), PublishedAt: optionalString(source["publish_date"])}})
		}
	}
	n.Usage = normalizeUsage(p.ID, root.Usage)
	return n, message, nil
}

func normalizeUsage(provider string, raw json.RawMessage) *Usage {
	u := optionalObject(raw)
	if u == nil {
		return nil
	}
	input, output := optionalObject(u["prompt_tokens_details"]), optionalObject(u["completion_tokens_details"])
	v := &Usage{InputTokens: optionalInt(u["prompt_tokens"]), OutputTokens: optionalInt(u["completion_tokens"]), TotalTokens: optionalInt(u["total_tokens"]), ReasoningTokens: optionalInt(output["reasoning_tokens"]), CachedInputTokens: optionalInt(input["cached_tokens"])}
	if provider == "deepseek" {
		if v.CachedInputTokens == nil {
			v.CachedInputTokens = optionalInt(u["prompt_cache_hit_tokens"])
		}
		v.UncachedInputTokens = optionalInt(u["prompt_cache_miss_tokens"])
	}
	if provider == "mimo" {
		search := optionalObject(u["web_search_usage"])
		v.SearchCalls, v.SearchPages = optionalInt(search["tool_usage"]), optionalInt(search["page_usage"])
	}
	return v
}

func safeURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}
func mergeSources(dst, src []analysis.Source) []analysis.Source {
	for _, s := range src {
		if !safeURL(s.URL) || len(dst) >= 100 {
			continue
		}
		found := false
		for _, existing := range dst {
			if existing.URL == s.URL {
				found = true
				break
			}
		}
		if !found {
			if s.Title == "" {
				s.Title = s.URL
			}
			dst = append(dst, s)
		}
	}
	return dst
}
