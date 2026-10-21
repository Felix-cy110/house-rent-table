package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

func TestTurnRedactsCredentialsInNestedJSONString(t *testing.T) {
	secret := "fixture-\"\\<>&-secret"
	for _, escapeHTML := range []bool{true, false} {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(escapeHTML)
		_ = encoder.Encode(map[string]string{secret: secret})
		turn := &turnLog{Input: json.RawMessage(`{}`), Events: []*turnEvent{{Kind: "tool", Output: encoded.String()}}}
		var decoded turnLog
		if json.Unmarshal([]byte(turn.encode(secret)), &decoded) != nil || len(decoded.Events) != 1 {
			t.Fatal("invalid redacted log")
		}
		var output map[string]string
		if json.Unmarshal([]byte(decoded.Events[0].Output), &output) != nil || output["[REDACTED]"] != "[REDACTED]" {
			t.Fatal("escaped credential survived in nested tool JSON")
		}
	}
}

func TestTurnCapturesFallbackAndSearchWithoutCredentials(t *testing.T) {
	fallback, err := NewSearchFallback("glm", "fixture-search-secret")
	if err != nil {
		t.Fatal(err)
	}
	phase := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == fallback.provider.SearchEndpoint {
			return fixtureResponse(200, `{"search_result":[{"title":"公示","link":"https://example.com/rent","content":"月租 2000 元"}],"vendor_extra":{"request_id":9007199254740993123}}`), nil
		}
		var body object
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tools"] == nil {
			return fixtureResponse(200, fixtureChat("probe-only", "", "", "", "")), nil
		}
		phase++
		switch (phase - 1) % 4 {
		case 0:
			return fixtureResponse(200, fixtureChat("", "rental_calculate", "calc1", `{"calculations":[{"label":"首次付款","expression":"2000*4"}]}`, "")), nil
		case 1:
			return fixtureResponse(403, `{"error":{"message":"Web Search plugin not activated; fixture-model-secret; fixture-search-secret"}}`), nil
		case 2:
			return fixtureResponse(200, fixtureChat("", searchToolName, "search1", `{"query":"测试区域 租金"}`, "")), nil
		default:
			return fixtureResponse(200, `{"id":"final-response","model":"returned-model","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"首次付款 8000 元","reasoning_content":null,"unknown":{"items":[false,0,null]}}}],"usage":{"total_tokens":42,"future_tokens":7}}`), nil
		}
	})}
	s := New(Options{ConfigDir: t.TempDir(), HTTPClient: client, SearchFallback: fallback})
	if _, err := s.Login(context.Background(), analysis.LoginRequest{Provider: "mimo", APIKey: "fixture-model-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"first-document", "second-document"} {
		payload := json.RawMessage(fmt.Sprintf(" {\"fields\":[],\"fileName\":%q} \n", label))
		result, err := s.Analyze(context.Background(), payload)
		if err != nil {
			t.Fatal(err)
		}
		var turn turnLog
		if json.Unmarshal([]byte(result.TurnJSON), &turn) != nil {
			t.Fatal("missing valid turn JSON")
		}
		if turn.SchemaVersion != 1 || turn.Provider != "mimo" || turn.Model != "returned-model" || turn.Status != Finished || turn.Result.Text != result.Text || turn.Result.TurnJSON != "" {
			t.Fatal("missing turn metadata or recursive report")
		}
		if turn.StartedAt.IsZero() || turn.CompletedAt.Before(turn.StartedAt) || turn.DurationMS < 0 || turn.Usage != nil {
			t.Fatal("invalid timing or fabricated aggregate usage for failed round")
		}
		kinds := []string{"model", "tool", "model", "model", "tool", "search", "model"}
		if len(turn.Events) != len(kinds) {
			t.Fatalf("got %d events, want %d", len(turn.Events), len(kinds))
		}
		for i, kind := range kinds {
			event := turn.Events[i]
			if event.Sequence != i+1 || event.Kind != kind || event.StartedAt.IsZero() || event.DurationMS < 0 {
				t.Fatalf("event %d has incorrect order, kind or timing", i)
			}
		}
		var request struct{ Messages []chatMessage }
		if json.Unmarshal(turn.Events[0].Request, &request) != nil || len(request.Messages) != 2 || *request.Messages[1].Content != string(payload) || !strings.Contains(*request.Messages[0].Content, "联网核价必须使用") {
			t.Fatal("first request snapshot lost original input or system instructions")
		}
		if turn.Events[1].ToolCallID != "calc1" || !strings.Contains(turn.Events[1].Output, "8000.00") || turn.Events[4].ToolCallID != "search1" || !strings.Contains(turn.Events[4].Output, "https://example.com/rent") {
			t.Fatal("tool arguments or outputs lost")
		}
		if turn.Events[2].HTTPStatus != 403 || turn.Events[2].Error == "" || !strings.Contains(string(turn.Events[2].Response), "[REDACTED]") {
			t.Fatal("fallback error response lost or credentials not redacted")
		}
		if turn.Events[5].URL != fallback.provider.SearchEndpoint || !strings.Contains(string(turn.Events[5].Request), "测试区域 租金") || !strings.Contains(string(turn.Events[5].Response), "9007199254740993123") {
			t.Fatal("search request, raw response or large numeric ID lost")
		}
		last := optionalObject(turn.Events[6].Response)
		if optionalString(last["id"]) != "final-response" || !strings.Contains(string(last["usage"]), "future_tokens") || !strings.Contains(string(last["choices"]), `"reasoning_content": null`) {
			t.Fatal("raw final response lost vendor metadata, usage or null reasoning")
		}
		for _, forbidden := range []string{"fixture-model-secret", "fixture-search-secret", "Authorization", "probe-only"} {
			if strings.Contains(result.TurnJSON, forbidden) {
				t.Fatalf("log contains forbidden data: %s", forbidden)
			}
		}
		if label == "second-document" && strings.Contains(result.TurnJSON, "first-document") {
			t.Fatal("turn log mixed independent analyses")
		}
	}
}

func TestTurnPreservesAggregateAndRawUsage(t *testing.T) {
	count := 0
	s, _ := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			chatReply(w, "OK", "stop")
			return
		}
		reply := fixtureChat("费用已核对", "", "", "", "")
		if count == 2 {
			reply = fixtureChat("", "rental_calculate", "calc1", `{"calculations":[{"label":"核算","expression":"1+2"}]}`, "")
		}
		_, _ = w.Write([]byte(reply))
	}, "mimo")
	result, err := s.Analyze(context.Background(), json.RawMessage(`{"fields":[]}`))
	var turn turnLog
	if err != nil || json.Unmarshal([]byte(result.TurnJSON), &turn) != nil || turn.Usage == nil || turn.Usage.TotalTokens == nil || *turn.Usage.TotalTokens != 20 {
		t.Fatal("aggregate token usage lost")
	}
	for _, index := range []int{0, 2} {
		usage := optionalObject(optionalObject(turn.Events[index].Response)["usage"])
		if string(usage["total_tokens"]) != "10" {
			t.Fatal("raw per-round usage lost")
		}
	}
}

func TestTurnRecordsInvalidAndTruncatedSearchResponses(t *testing.T) {
	for _, body := range []string{"<html>upstream failure</html>", strings.Repeat("x", (16<<10)+10)} {
		turn := &turnLog{}
		ctx := context.WithValue(context.Background(), turnContextKey{}, turn)
		s := New(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return fixtureResponse(503, body), nil
		})}})
		_, err := s.post(ctx, "test-secret", "https://example.com/search", map[string]string{"query": "租金"})
		if err == nil || len(turn.Events) != 1 {
			t.Fatal("failed request not recorded")
		}
		event := turn.Events[0]
		if event.HTTPStatus != 503 || event.Error == "" || len(event.Response) != 0 || event.ResponseText != body[:min(len(body), 16<<10)] || event.Truncated != (len(body) > 16<<10) {
			t.Fatal("non-JSON response or truncation not represented accurately")
		}
	}
}
