package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
)

const inactivePlugin = `{"error":{"message":"Web Search plugin has not been activated; upstream-private-detail"}}`

func fixtureResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func fixtureChat(text, name, id, arguments, searchError string) string {
	m := map[string]any{"role": "assistant", "content": text, "reasoning_content": "private-reasoning", "error_message": searchError}
	finish := "stop"
	if name != "" {
		finish = "tool_calls"
		m["content"] = nil
		m["tool_calls"] = []any{map[string]any{"id": id, "type": "function", "function": map[string]string{"name": name, "arguments": arguments}}}
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": m}}, "usage": map[string]any{"total_tokens": 10}})
	return string(raw)
}

func TestSearchFallbackConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name, provider, key, want string
		invalid                   bool
	}{
		{"disabled", "", "", "", false},
		{"default", "", "fixture-search-key", "glm", false},
		{"glm", " glm ", " fixture-search-key ", "glm", false},
		{"deepseek", "deepseek", "fixture-search-key", "deepseek", false},
		{"missing-key", "glm", "", "", true},
		{"unknown", "unknown", "fixture-search-key", "", true},
		{"mimo", "mimo", "fixture-search-key", "", true},
		{"control", "glm", "fixture-search-key\n", "", true},
		{"oversized", "glm", strings.Repeat("x", 8193), "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewSearchFallback(tt.provider, tt.key)
			if (err != nil) != tt.invalid || f.provider.ID != tt.want {
				t.Fatalf("configuration: provider=%q err=%v", f.provider.ID, err)
			}
			raw, _ := json.Marshal(f)
			if string(raw) != "{}" || err != nil && strings.Contains(err.Error(), "fixture-search-key") {
				t.Fatal("configuration exposed credential")
			}
		})
	}
}

func TestMiMoFallbackContinuesWithIsolatedCredentials(t *testing.T) {
	for _, backend := range []string{"glm", "deepseek"} {
		for _, signal := range []string{"http400", "http403", "http422", "root-error", "final-error", "empty-error", "null-error", "tool-error"} {
			t.Run(backend+"/"+signal, func(t *testing.T) {
				fallback, err := NewSearchFallback(backend, "fixture-search-key")
				if err != nil {
					t.Fatal(err)
				}
				phase, searches := 0, 0
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.String() == fallback.provider.SearchEndpoint {
						searches++
						if r.Header.Get("Authorization") != "Bearer fixture-search-key" {
							t.Fatal("model key forwarded to search")
						}
						var request object
						_ = json.NewDecoder(r.Body).Decode(&request)
						if backend == "glm" {
							if optionalString(request["search_query"]) != "测试区域 租金" || optionalString(request["search_engine"]) != "search_pro" {
								t.Fatal("wrong GLM search request")
							}
							return fixtureResponse(200, `{"search_result":[{"title":"测试公示","link":"https://example.com/fees","content":"月租 2000"}]}`), nil
						}
						if optionalString(request["query"]) != "测试区域 租金" || string(request["need_content"]) != "false" {
							t.Fatal("wrong DeepSeek search request")
						}
						return fixtureResponse(200, `{"results":[{"title":"测试公示","url":"https://example.com/fees","summary":"月租 2000"}]}`), nil
					}
					if r.URL.String() != "https://api.xiaomimimo.com/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-model-key" {
						t.Fatal("inference model endpoint or key changed")
					}
					var body object
					_ = json.NewDecoder(r.Body).Decode(&body)
					assertThinking(t, "mimo", body)
					if optionalString(body["model"]) != "mimo-v2.6-flash" || strings.Contains(string(body["messages"]), "fixture-search-key") {
						t.Fatal("model or credential isolation broken")
					}
					if len(body["tools"]) == 0 {
						return fixtureResponse(200, fixtureChat("OK", "", "", "", "")), nil
					}
					var specs []object
					var messages []chatMessage
					_ = json.Unmarshal(body["tools"], &specs)
					_ = json.Unmarshal(body["messages"], &messages)
					if len(specs) != 2 {
						t.Fatal("missing tools")
					}
					if phase < 2 {
						if optionalString(specs[1]["type"]) != "web_search" {
							t.Fatal("did not try hosted search first")
						}
					} else {
						function := optionalObject(specs[1]["function"])
						query := optionalObject(optionalObject(optionalObject(function["parameters"])["properties"])["query"])
						limit := optionalInt(query["maxLength"])
						if optionalString(specs[1]["type"]) != "function" || optionalString(function["name"]) != searchToolName || limit == nil || int(*limit) != fallback.provider.SearchQueryLimit {
							t.Fatal("fallback search schema differs from selected backend")
						}
					}
					if phase > 0 && (len(messages) < 4 || optionalString(messages[2].Reasoning) != "private-reasoning" || messages[3].ToolCallID != "calc1" || !strings.Contains(*messages[3].Content, "8000.00")) {
						t.Fatal("completed calculator history was lost or replayed")
					}
					phase++
					switch phase {
					case 1:
						return fixtureResponse(200, fixtureChat("", "rental_calculate", "calc1", `{"calculations":[{"label":"押一付三","expression":"2000*(1+3)"}]}`, "")), nil
					case 2:
						switch signal {
						case "http400":
							return fixtureResponse(400, inactivePlugin), nil
						case "http403":
							return fixtureResponse(403, inactivePlugin), nil
						case "http422":
							return fixtureResponse(422, inactivePlugin), nil
						case "root-error":
							return fixtureResponse(200, inactivePlugin), nil
						case "final-error":
							return fixtureResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":"discard-unverified-report","error_message":"联网搜索插件未开通","annotations":[{"type":"url_citation","url":"https://example.com/unverified"}]}}],"usage":{"total_tokens":10}}`), nil
						case "empty-error":
							return fixtureResponse(200, fixtureChat("", "", "", "", "联网搜索插件未开通")), nil
						case "null-error":
							return fixtureResponse(200, `{"choices":[{"finish_reason":"stop","message":{"content":null,"error_message":"联网搜索插件未开通"}}],"usage":{"total_tokens":10}}`), nil
						default:
							return fixtureResponse(200, fixtureChat("", "rental_calculate", "calc2", `{"calculations":[{"label":"月租","expression":"2000"}]}`, "Web Search plugin is not enabled")), nil
						}
					case 3:
						if signal == "tool-error" && (len(messages) != 6 || messages[5].ToolCallID != "calc2" || !strings.Contains(*messages[5].Content, "2000.00")) {
							t.Fatal("pending tool call was not completed before fallback continuation")
						}
						return fixtureResponse(200, fixtureChat("", searchToolName, "search1", `{"query":"测试区域 租金"}`, "")), nil
					case 4:
						last := messages[len(messages)-1]
						if last.ToolCallID != "search1" || !strings.Contains(*last.Content, "https://example.com/fees") {
							t.Fatal("search results not continued")
						}
						return fixtureResponse(200, fixtureChat("押一付三合计 8000.00 元", "", "", "", "")), nil
					default:
						t.Fatal("unexpected retry")
						return nil, errors.New("unexpected retry")
					}
				})}
				dir := t.TempDir()
				s := New(Options{ConfigDir: dir, HTTPClient: client, SearchFallback: fallback})
				connection := analysis.LoginRequest{Provider: "mimo", APIKey: "fixture-model-key"}
				if _, err := s.Login(context.Background(), connection); err != nil {
					t.Fatal(err)
				}
				result, err := s.complete(context.Background(), connection, "核算租金", "月租 2000", nil, true)
				if err != nil || phase != 4 || searches != 1 || result.Provider != "mimo" || result.Text != "押一付三合计 8000.00 元" || len(result.Sources) != 1 || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "备用搜索") {
					t.Fatalf("unexpected result: %+v, err=%v, rounds=%d searches=%d", result, err, phase, searches)
				}
				if signal == "final-error" || signal == "tool-error" || signal == "empty-error" || signal == "null-error" {
					if result.Usage == nil || result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 40 {
						t.Fatal("successful error response usage not counted")
					}
				} else if result.Usage != nil {
					t.Fatal("unknown failed request usage presented as complete")
				}
				saved, err := os.ReadFile(filepath.Join(dir, configFile))
				raw, _ := json.Marshal(result)
				for _, secret := range []string{"fixture-search-key", "private-reasoning", "upstream-private-detail", "discard-unverified-report"} {
					if err != nil || strings.Contains(string(saved), secret) || strings.Contains(string(raw), secret) {
						t.Fatal("private fallback data leaked")
					}
				}
			})
		}
	}
}

func TestMiMoFallbackDoesNotMaskOtherFailures(t *testing.T) {
	for _, tt := range []struct {
		name, provider             string
		status                     int
		body                       string
		tools, configured, success bool
		want                       error
	}{
		{"hosted-success", "mimo", 200, fixtureChat("OK", "", "", "", ""), true, true, true, nil},
		{"content-is-not-error", "mimo", 200, fixtureChat("Web Search plugin not enabled", "", "", "", ""), true, true, true, nil},
		{"generic403", "mimo", 403, `{"error":{"message":"API Key restricted by risk control"}}`, true, true, false, analysis.ErrFailed},
		{"401", "mimo", 401, inactivePlugin, true, true, false, analysis.ErrFailed},
		{"402", "mimo", 402, inactivePlugin, true, true, false, analysis.ErrFailed},
		{"429", "mimo", 429, inactivePlugin, true, true, false, analysis.ErrFailed},
		{"500", "mimo", 500, inactivePlugin, true, true, false, analysis.ErrFailed},
		{"refused", "mimo", 200, `{"choices":[{"finish_reason":"content_filter","message":{"content":"refused","error_message":"Web Search plugin not enabled"}}]}`, true, true, false, analysis.ErrFailed},
		{"incomplete", "mimo", 200, `{"choices":[{"finish_reason":"length","message":{"content":"partial","error_message":"Web Search plugin not enabled"}}]}`, true, true, false, analysis.ErrFailed},
		{"malformed", "mimo", 200, `{"choices":[{"finish_reason":"stop","message":{"content":123,"error_message":"Web Search plugin not enabled"}}]}`, true, true, false, analysis.ErrFailed},
		{"malformed-tools", "mimo", 200, `{"choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":[],"error_message":"Web Search plugin not enabled"}}]}`, true, true, false, analysis.ErrFailed},
		{"empty-answer", "mimo", 200, fixtureChat("", "", "", "", ""), true, true, false, analysis.ErrFailed},
		{"invalid-json", "mimo", 403, `Web Search plugin not enabled`, true, true, false, analysis.ErrFailed},
		{"oversized-error", "mimo", 403, `{"message":"Web Search plugin not enabled","detail":"` + strings.Repeat("x", 16<<10) + `"}`, true, true, false, analysis.ErrFailed},
		{"no-config", "mimo", 403, inactivePlugin, true, false, false, analysis.ErrSearchUnavailable},
		{"no-tools-http", "mimo", 403, inactivePlugin, false, true, false, analysis.ErrFailed},
		{"no-tools-root", "mimo", 200, inactivePlugin, false, true, false, analysis.ErrFailed},
		{"other-provider", "glm", 403, inactivePlugin, true, true, false, analysis.ErrFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var f SearchFallback
			if tt.configured {
				f, _ = NewSearchFallback("glm", "fixture-search-key")
			}
			calls := 0
			s := New(Options{SearchFallback: f, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return fixtureResponse(tt.status, tt.body), nil
			})}})
			_, err := s.complete(context.Background(), analysis.LoginRequest{Provider: tt.provider, APIKey: "fixture-model-key"}, "test", "test", nil, tt.tools)
			if calls != 1 || (err == nil) != tt.success || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestMiMoFallbackIsBoundedAndCancelable(t *testing.T) {
	for _, mode := range []string{"repeated-signal", "budget", "search-fails", "cancel-before-switch", "cancel-search", "network-timeout"} {
		t.Run(mode, func(t *testing.T) {
			fallback, _ := NewSearchFallback("glm", "fixture-search-key")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rounds, searches := 0, 0
			s := New(Options{SearchFallback: fallback, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == fallback.provider.SearchEndpoint {
					searches++
					if mode == "cancel-search" {
						cancel()
						return nil, context.Canceled
					}
					return fixtureResponse(403, `{"message":"upstream-private-detail"}`), nil
				}
				rounds++
				if mode == "network-timeout" {
					return nil, context.DeadlineExceeded
				}
				if rounds == 1 || mode == "repeated-signal" {
					if mode == "cancel-before-switch" {
						cancel()
					}
					return fixtureResponse(403, inactivePlugin), nil
				}
				if mode == "search-fails" && rounds == 3 {
					return fixtureResponse(200, fixtureChat("费用核算完成，市价未能核实", "", "", "", "")), nil
				}
				return fixtureResponse(200, fixtureChat("", searchToolName, "search", `{"query":"区域 租金"}`, "")), nil
			})}})
			result, err := s.complete(ctx, analysis.LoginRequest{Provider: "mimo", APIKey: "fixture-model-key"}, "test", "test", nil, true)
			switch mode {
			case "repeated-signal":
				if rounds != 2 || searches != 0 || err == nil {
					t.Fatal("fallback was retried more than once")
				}
			case "budget":
				if rounds != s.toolRoundLimit() || searches != s.opts.SearchLimit || err == nil {
					t.Fatalf("budgets reset: rounds=%d searches=%d err=%v", rounds, searches, err)
				}
			case "search-fails":
				if rounds != 3 || searches != 1 || err != nil || !strings.Contains(strings.Join(result.Warnings, " "), "联网搜索失败") {
					t.Fatalf("calculation did not survive failed search: %+v %v", result, err)
				}
			case "cancel-before-switch", "cancel-search":
				if !errors.Is(err, context.Canceled) || mode == "cancel-before-switch" && rounds != 1 || mode == "cancel-search" && rounds != 2 {
					t.Fatalf("cancellation ignored: rounds=%d err=%v", rounds, err)
				}
			case "network-timeout":
				if rounds != 1 || searches != 0 || err == nil {
					t.Fatal("timeout incorrectly triggered fallback")
				}
			}
		})
	}
}

func TestInactiveSearchMessageClassification(t *testing.T) {
	for message, want := range map[string]bool{
		"Web_Search plugin not enabled":                         true,
		"The Web Search Plugin must be activated prior to use.": true,
		"请先开通联网搜索插件":                                            true,
		"web search timed out":                                  false,
		"API key not enabled":                                   false,
		"web search quota exceeded":                             false,
		"web search permission denied":                          false,
	} {
		if searchPluginInactive(message) != want {
			t.Errorf("wrong classification for %q", message)
		}
	}
	// Provider-specific error metadata never affects other suppliers.
	p, _ := modelconfig.Lookup("glm")
	n, _, err := normalize(p, []byte(fixtureChat("OK", "", "", "", "Web Search plugin not enabled")))
	if err != nil || n.HostedSearchUnavailable {
		t.Fatal("MiMo signal applied to GLM")
	}
}
