package aiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
)

func TestParseSearchLimit(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  int
	}{
		{"", 24}, {"  ", 24}, {"1", 1}, {" 48 ", 48}, {"100", 100},
		{"0", 0}, {"-1", 0}, {"101", 0}, {"1.5", 0}, {"fixture-secret", 0},
	} {
		got, err := ParseSearchLimit(tt.input)
		if got != tt.want || (err != nil) != (tt.want == 0) || err != nil && strings.Contains(err.Error(), "fixture-secret") {
			t.Fatalf("setting %q: got %d, err %v", tt.input, got, err)
		}
	}
}

func TestSearchBudgetCountsRequestsAndReusesOnlySuccessfulResults(t *testing.T) {
	p, _ := modelconfig.Lookup("glm")
	calls := 0
	s := New(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return fixtureResponse(503, `{}`), nil
		}
		return fixtureResponse(200, `{"search_result":[{"title":"公示","link":"https://example.com/fees","content":"费用公示"}]}`), nil
	})}})
	budget := &searchBudget{limit: 2}
	if _, _, warning := s.search(context.Background(), "key", p, `{}`, budget); warning == "" || budget.calls != 0 || calls != 0 {
		t.Fatal("invalid arguments charged")
	}
	if _, _, warning := s.search(context.Background(), "key", p, `{"query":"区域 租金"}`, budget); warning == "" || budget.calls != 1 {
		t.Fatal("failed outbound request not charged")
	}
	output, sources, warning := s.search(context.Background(), "key", p, `{"query":"区域 租金"}`, budget)
	if warning != "" || calls != 2 || budget.calls != 2 || len(sources) != 1 {
		t.Fatal("failed result cached or successful result lost")
	}
	repeated, repeatedSources, warning := s.search(context.Background(), "key", p, `{"query":"  区域 租金  "}`, budget)
	if warning != "" || repeated != output || len(repeatedSources) != 1 || calls != 2 || budget.calls != 2 {
		t.Fatal("identical successful search charged again at limit")
	}
	if _, _, warning := s.search(context.Background(), "key", p, `{"query":"区域 水费"}`, budget); !strings.Contains(warning, "2 次") || calls != 2 {
		t.Fatal("configured limit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, warning := s.search(ctx, "key", p, `{"query":"区域 租金"}`, budget); warning == "" || calls != 2 {
		t.Fatal("canceled search reused cache")
	}
	// A new analysis must retrieve fresh results even for the same query.
	if _, _, warning := s.search(context.Background(), "different-key", p, `{"query":"区域 租金"}`, &searchBudget{limit: 2}); warning != "" || calls != 3 {
		t.Fatal("cache leaked between analyses")
	}
}

func TestEmptySearchResultsAreReused(t *testing.T) {
	p, _ := modelconfig.Lookup("deepseek")
	calls := 0
	s := New(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return fixtureResponse(200, `{"results":[]}`), nil
	})}})
	budget := &searchBudget{limit: 2}
	for i := 0; i < 2; i++ {
		if _, sources, warning := s.search(context.Background(), "key", p, `{"query":"区域 租金"}`, budget); warning != "" || len(sources) != 0 {
			t.Fatal("empty successful search changed")
		}
	}
	if calls != 1 || budget.calls != 1 {
		t.Fatal("empty results repeatedly consumed budget")
	}
}

func TestAnalysisCanSearchPastEightAndCalculateAfterConfiguredLimit(t *testing.T) {
	for _, provider := range []string{"glm", "deepseek", "mimo"} {
		for _, limit := range []int{0, 3} {
			t.Run(fmt.Sprintf("%s/%d", provider, limit), func(t *testing.T) {
				actualLimit := limit
				if actualLimit == 0 {
					actualLimit = 24
				}
				p, _ := modelconfig.Lookup(provider)
				searchProvider := p
				fallback, _ := NewSearchFallback("glm", "search-key")
				if provider == "mimo" {
					searchProvider = fallback.provider
				}
				searches, phase, attempts := 0, 0, 0
				s := New(Options{SearchLimit: limit, SearchFallback: fallback, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.String() == searchProvider.SearchEndpoint {
						searches++
						wantKey := "Bearer model-key"
						if provider == "mimo" {
							wantKey = "Bearer search-key"
						}
						if r.Header.Get("Authorization") != wantKey {
							t.Fatal("wrong search credential")
						}
						return fixtureResponse(200, `{"search_result":[{"link":"https://example.com/fees"}],"results":[{"url":"https://example.com/fees"}]}`), nil
					}
					attempts++
					var body object
					_ = json.NewDecoder(r.Body).Decode(&body)
					assertThinking(t, provider, body)
					if r.URL.String() != p.Endpoint || r.Header.Get("Authorization") != "Bearer model-key" {
						t.Fatal("model changed")
					}
					if provider == "mimo" && attempts == 1 {
						return fixtureResponse(403, inactivePlugin), nil
					}
					var specs []object
					var messages []chatMessage
					_ = json.Unmarshal(body["tools"], &specs)
					_ = json.Unmarshal(body["messages"], &messages)
					if searches == actualLimit {
						if len(specs) != 1 || optionalString(optionalObject(specs[0]["function"])["name"]) != "rental_calculate" {
							t.Fatal("exhausted search still advertised or calculator removed")
						}
						if phase == actualLimit {
							phase++
							return fixtureResponse(200, fixtureChat("", "rental_calculate", "calc", `{"calculations":[{"label":"合计","expression":"2000*4"}]}`, "")), nil
						}
						if !strings.Contains(*messages[len(messages)-1].Content, "8000.00") {
							t.Fatal("calculation lost after search cap")
						}
						return fixtureResponse(200, fixtureChat("核算完成 8000.00 元", "", "", "", "")), nil
					}
					if len(specs) != 2 {
						t.Fatal("search disabled early")
					}
					phase++
					return fixtureResponse(200, fixtureChat("", searchToolName, fmt.Sprintf("s%d", phase), fmt.Sprintf(`{"query":"区域 费用%d"}`, phase), "")), nil
				})}})
				result, err := s.complete(context.Background(), analysis.LoginRequest{Provider: provider, APIKey: "model-key"}, "核价", "test", nil, true)
				if err != nil || searches != actualLimit || !strings.Contains(result.Text, "8000.00") || len(result.Sources) != 1 || !strings.Contains(strings.Join(result.Warnings, " "), fmt.Sprintf("%d 次上限", actualLimit)) {
					t.Fatalf("search cap prevented completed report: searches=%d result=%+v err=%v", searches, result, err)
				}
			})
		}
	}
}

func TestToolRoundLimitReservesFinalAnswer(t *testing.T) {
	rounds := 0
	s := New(Options{SearchLimit: 1, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rounds++
		var body object
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body["tools"]) == 0 {
			if !strings.Contains(string(body["messages"]), "8000.00") {
				t.Fatal("final answer lost calculation history")
			}
			return fixtureResponse(200, fixtureChat("已有费用合计 8000.00 元，市价未能核实", "", "", "", "")), nil
		}
		return fixtureResponse(200, fixtureChat("", "rental_calculate", "calc", `{"calculations":[{"label":"合计","expression":"2000*4"}]}`, "")), nil
	})}})
	result, err := s.complete(context.Background(), analysis.LoginRequest{Provider: "glm", APIKey: "key"}, "核算", "test", nil, true)
	if err != nil || rounds != s.toolRoundLimit() || !strings.Contains(result.Text, "8000.00") || !strings.Contains(strings.Join(result.Warnings, " "), "工具交互") {
		t.Fatalf("no final answer at round limit: %+v %v rounds=%d", result, err, rounds)
	}
}
