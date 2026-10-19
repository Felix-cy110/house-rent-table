package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
)

func assertThinking(t *testing.T, provider string, body map[string]json.RawMessage) {
	t.Helper()
	thinking := optionalObject(body["thinking"])
	if optionalString(thinking["type"]) != "enabled" {
		t.Fatal("thinking not explicitly enabled")
	}
	if provider == "glm" && (string(thinking["clear_thinking"]) != "false" || optionalString(body["reasoning_effort"]) != "max") {
		t.Fatal("GLM thinking configuration missing")
	}
	if provider == "deepseek" && optionalString(body["reasoning_effort"]) != "high" {
		t.Fatal("DeepSeek thinking effort missing")
	}
	if body["temperature"] != nil || body["top_p"] != nil {
		t.Fatal("unsupported sampling override")
	}
}

func TestProviderEndpointsThinkingAndReasoningContinuation(t *testing.T) {
	endpoints := map[string]string{"mimo": "https://api.xiaomimimo.com/v1/chat/completions", "glm": "https://open.bigmodel.cn/api/paas/v4/chat/completions", "deepseek": "https://api.deepseek.com/chat/completions"}
	for _, provider := range modelconfig.All() {
		t.Run(provider.ID, func(t *testing.T) {
			var calls int
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != endpoints[provider.ID] || r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Fatal("endpoint or auth differs from official configuration")
				}
				var body map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Fatal("invalid request")
				}
				assertThinking(t, provider.ID, body)
				if optionalString(body["model"]) != provider.Model {
					t.Fatal("incorrect model")
				}
				w := httptest.NewRecorder()
				if calls == 1 {
					chatReply(w, "OK", "stop")
					return w.Result(), nil
				}
				var specs []map[string]json.RawMessage
				_ = json.Unmarshal(body["tools"], &specs)
				if len(specs) != 2 {
					t.Fatal("missing calculation/search tools")
				}
				switch provider.ID {
				case "mimo":
					if optionalString(specs[1]["type"]) != "web_search" || string(specs[1]["max_keyword"]) != "3" {
						t.Fatal("wrong MiMo search")
					}
				case "glm", "deepseek":
					if optionalString(specs[1]["type"]) != "function" {
						t.Fatal("DeepSeek must use Search API function")
					}
				}
				if calls == 2 {
					_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_content":"仅供续传\n保留 原文 🏠","tool_calls":[{"id":"calc1","type":"function","function":{"name":"rental_calculate","arguments":"{\"calculations\":[{\"label\":\"测试\",\"expression\":\"0.1+0.2\"}]}"}}],"future_extension":{"ignore":true}}}]}`))
				} else {
					var messages []chatMessage
					_ = json.Unmarshal(body["messages"], &messages)
					if len(messages) != 4 || optionalString(messages[2].Reasoning) != "仅供续传\n保留 原文 🏠" || messages[2].Content != nil || messages[3].ToolCallID != "calc1" || !strings.Contains(*messages[3].Content, "0.30") {
						t.Fatal("reasoning or tool result lost")
					}
					chatReply(w, "结论 0.30 元", "stop")
				}
				return w.Result(), nil
			})}
			s := New(Options{ConfigDir: t.TempDir(), HTTPClient: client})
			if _, err := s.Login(context.Background(), analysis.LoginRequest{Provider: provider.ID, APIKey: "fixture-key"}); err != nil {
				t.Fatal(err)
			}
			result, err := s.Analyze(context.Background(), json.RawMessage(`{"fields":[]}`))
			raw, _ := json.Marshal(result)
			if err != nil || result.Text != "结论 0.30 元" || calls != 3 || strings.Contains(string(raw), "仅供续传") || len(result.Warnings) != 1 {
				t.Fatalf("unexpected normalized report: %+v %v", result, err)
			}
		})
	}
}

func TestOptionalProviderFieldsNormalize(t *testing.T) {
	for _, tt := range []struct {
		provider, body    string
		cached, reasoning int64
		sources           int
	}{
		{"mimo", `{"choices":[{"finish_reason":"stop","message":{"content":"参考🏠","reasoning_content":null,"tool_calls":null,"annotations":[{"type":"url_citation","url":"https://example.com/a","title":null,"start_index":0,"end_index":3},{"type":"url_citation","url":"javascript:alert(1)"},null,{"type":"future","url":"https://example.com/b"}]}}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":4},"web_search_usage":{"tool_usage":2,"page_usage":3}},"future":true}`, 0, 4, 1},
		{"glm", `{"model":"glm-returned-version","choices":[{"finish_reason":"stop","message":{"content":"参考","reasoning_content":"private","refusal":null}}],"web_search":[{"title":"物业公示","link":"https://example.com/g","publish_date":null,"refer":"ref_1"},{"link":"https://example.com/g"},{"link":"https://user:password@example.com/"}],"usage":{"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":null}}`, 5, -1, 1},
		{"deepseek", `{"model":null,"choices":[{"finish_reason":"stop","message":{"content":"结论","tool_calls":[]}}],"usage":{"prompt_cache_hit_tokens":9,"prompt_cache_miss_tokens":11,"completion_tokens_details":{"reasoning_tokens":3},"prompt_tokens_details":null}}`, 9, 3, 0},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			p, _ := modelconfig.Lookup(tt.provider)
			n, _, err := normalize(p, []byte(tt.body))
			if err != nil || n.Finish != Finished || n.Provider != p.ID || len(n.Sources) != tt.sources || n.Usage == nil || n.Usage.CachedInputTokens == nil || *n.Usage.CachedInputTokens != tt.cached {
				t.Fatalf("normalize: %+v %v", n, err)
			}
			if tt.reasoning < 0 {
				if n.Usage.ReasoningTokens != nil {
					t.Fatal("missing usage converted to zero")
				}
			} else if n.Usage.ReasoningTokens == nil || *n.Usage.ReasoningTokens != tt.reasoning {
				t.Fatal("reasoning usage lost")
			}
			if tt.provider == "mimo" && (len(n.Citations) != 1 || n.Citations[0].EndIndex != 3 || *n.Usage.SearchCalls != 2) {
				t.Fatal("MiMo search/citation normalization failed")
			}
			if tt.provider == "deepseek" && (n.Model != p.Model || *n.Usage.UncachedInputTokens != 11) {
				t.Fatal("DeepSeek optional model/cache normalization failed")
			}
		})
	}
	p, _ := modelconfig.Lookup("mimo")
	for _, extra := range []string{``, `,"usage":null`, `,"usage":{"prompt_tokens":"broken","completion_tokens":-1,"prompt_tokens_details":[]}`, `,"usage":"unexpected"`} {
		n, _, err := normalize(p, []byte(`{"choices":[{"finish_reason":"stop","message":{"content":"OK","annotations":null}}]`+extra+`}`))
		if err != nil || n.Text != "OK" || n.Usage != nil && n.Usage.InputTokens != nil {
			t.Fatal("optional metadata broke successful reply or fabricated usage")
		}
	}
}

func TestInvalidEssentialFieldsAndFinishReasons(t *testing.T) {
	p, _ := modelconfig.Lookup("glm")
	for _, body := range []string{
		`{}`, `null`, `{"choices":[]}`, `{"choices":[{},{}]}`, `{"choices":[{"finish_reason":"stop","message":{"content":[]}}]}`,
		`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[]}}]}`,
		`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"type":"function","function":{"name":"a","arguments":"{}"}}]}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":"OK","reasoning_content":{}}}]}`,
		`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"a","type":"function","function":{"name":"a"}},{"id":"a","type":"function","function":{"name":"b"}}]}}]}`,
	} {
		if _, _, err := normalize(p, []byte(body)); err == nil {
			t.Fatalf("accepted malformed essential fields: %s", body)
		}
	}
	for reason, want := range map[string]Finish{"length": Incomplete, "sensitive": Refused, "content_filter": Refused, "repetition_truncation": Incomplete, "network_error": Failed, "future_reason": Failed, "model_context_window_exceeded": Incomplete} {
		n, _, err := normalize(p, []byte(`{"choices":[{"finish_reason":"`+reason+`","message":{"content":"partial"}}]}`))
		if err != nil || n.Finish != want {
			t.Errorf("finish %s: %s %v", reason, n.Finish, err)
		}
	}
}

func TestSearchRoundTripAndFailures(t *testing.T) {
	for _, provider := range []string{"glm", "deepseek"} {
		t.Run(provider, func(t *testing.T) {
			for _, mode := range []string{"success", "empty", "denied", "malformed"} {
				t.Run(mode, func(t *testing.T) {
					var calls atomic.Int32
					s, _ := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
						var body map[string]json.RawMessage
						_ = json.NewDecoder(r.Body).Decode(&body)
						if r.URL.Path == "/v1/tools/search" || r.URL.Path == "/api/paas/v4/web_search" {
							calls.Add(1)
							if provider == "deepseek" && (r.URL.Path != "/v1/tools/search" || optionalString(body["query"]) != "杭州 小区 物业费" || string(body["need_content"]) != "false") {
								t.Error("wrong DeepSeek search query")
							}
							if provider == "glm" && (r.URL.Path != "/api/paas/v4/web_search" || optionalString(body["search_query"]) != "杭州 小区 物业费" || optionalString(body["search_engine"]) != "search_pro" || string(body["search_intent"]) != "false") {
								t.Error("wrong GLM search query")
							}
							switch mode {
							case "success":
								if provider == "glm" {
									_, _ = w.Write([]byte(`{"search_result":[{"title":"公示","link":"https://example.com/fees","content":"每月2元","media":null,"publish_date":"2026-10-01","refer":"ref_1"}]}`))
								} else {
									_, _ = w.Write([]byte(`{"results":[{"title":"公示","url":"https://example.com/fees","summary":"每月2元","site_name":null,"date":"2026-10-01","future":true}]}`))
								}
							case "empty":
								_, _ = w.Write([]byte(`{"results":[],"search_result":[]}`))
							case "denied":
								w.WriteHeader(403)
								_, _ = w.Write([]byte(`{"error":"secret"}`))
							case "malformed":
								_, _ = w.Write([]byte(`{"results":null,"search_result":null}`))
							}
							return
						}
						assertThinking(t, provider, body)
						if len(body["tools"]) == 0 {
							chatReply(w, "OK", "stop")
							return
						}
						var messages []chatMessage
						_ = json.Unmarshal(body["messages"], &messages)
						if len(messages) == 2 {
							_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":null,"reasoning_content":"private","tool_calls":[{"id":"s1","type":"function","function":{"name":"rental_web_search","arguments":"{\"query\":\"杭州 小区 物业费\"}"}}]}}]}`))
							return
						}
						last := messages[len(messages)-1]
						if last.Role != "tool" || last.ToolCallID != "s1" || strings.Contains(*last.Content, "secret") {
							t.Fatal("unsafe search continuation")
						}
						if mode == "success" && !strings.Contains(*last.Content, "每月2元") {
							t.Fatal("search result lost")
						}
						chatReply(w, "已完成费用核算。", "stop")
					}, provider)
					result, err := s.Analyze(context.Background(), []byte(`{}`))
					if err != nil || calls.Load() != 1 {
						t.Fatalf("search failed: %+v %v", result, err)
					}
					if mode == "success" {
						if len(result.Sources) != 1 || len(result.Warnings) != 0 {
							t.Fatal("search source lost")
						}
					} else if len(result.Warnings) == 0 || len(result.Sources) != 0 {
						t.Fatal("search failure hidden")
					}
				})
			}
		})
	}
}

func TestSearchLimitsAndUnknownToolsNeverInvokeNetwork(t *testing.T) {
	p, _ := modelconfig.Lookup("deepseek")
	s := New(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid search invoked network")
		return nil, errors.New("unexpected")
	})}})
	for _, args := range []string{`{}`, `{"query":" "}`, `{"query":"x","url":"https://elsewhere"}`, `{"query":"x"}{}`, `{"query":"` + strings.Repeat("字", 129) + `"}`} {
		_, _, warning := s.search(context.Background(), "key", p, args, 1)
		if warning == "" {
			t.Fatal("invalid search accepted")
		}
	}
	if _, _, warning := s.search(context.Background(), "key", p, `{"query":"正常"}`, 9); warning == "" {
		t.Fatal("search budget ignored")
	}
	if !strings.Contains(calculate("shell", "{}"), "不允许") {
		t.Fatal("arbitrary tool accepted")
	}
}

func TestLegacyConfigRequiresExplicitReconnection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFile)
	old := []byte(`{"apiKey":"legacy-secret","baseUrl":"https://old-gateway.example/v1","model":"mimo-v2.6-flash"}`)
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{ConfigDir: dir})
	account, err := s.Account(context.Background())
	if err != nil || account.Connected || !account.NeedsReconnect {
		t.Fatalf("legacy configuration not retired: %+v %v", account, err)
	}
	if _, err = s.Analyze(context.Background(), []byte(`{}`)); !errors.Is(err, analysis.ErrLoginRequired) {
		t.Fatal("legacy key reused")
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(old) {
		t.Fatal("legacy configuration overwritten")
	}
	if err = s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	account, _ = s.Account(context.Background())
	if account.NeedsReconnect {
		t.Fatal("clear did not reset migration state")
	}
}

func TestUsageNumberVariantsAndUnknownTotals(t *testing.T) {
	p, _ := modelconfig.Lookup("glm")
	n, _, err := normalize(p, []byte(`{"choices":[{"finish_reason":"stop","message":{"content":"OK"}}],"usage":{"prompt_tokens":12.0,"completion_tokens":1e3,"total_tokens":1.5,"prompt_tokens_details":{"cached_tokens":0}}}`))
	if err != nil || n.Usage == nil || n.Usage.InputTokens == nil || *n.Usage.InputTokens != 12 || n.Usage.OutputTokens == nil || *n.Usage.OutputTokens != 1000 || n.Usage.TotalTokens != nil || *n.Usage.CachedInputTokens != 0 {
		t.Fatal("number normalization lost unknown/zero semantics")
	}
	if addUsage(n.Usage, nil) != nil {
		t.Fatal("partial usage presented as complete")
	}
	for _, raw := range []string{`-1`, `1.2`, `9223372036854775808`, `1e999999999`, `1e-999999999`, `null`, `"3"`, `{}`} {
		if optionalInt([]byte(raw)) != nil {
			t.Fatal("invalid token count accepted")
		}
	}
}
