package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/extraction"
)

func extractionFixture(t *testing.T) (extraction.Input, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../../docs/design/rental-persistence-v1/input.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var input extraction.Input
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile("../../docs/design/rental-persistence-v1/extraction.example.json")
	if err != nil {
		t.Fatal(err)
	}
	return input, output
}

func TestExtractionUsesIndependentGateSchemaAndTools(t *testing.T) {
	s, capture := testService(t)
	input, output := extractionFixture(t)
	if err := os.WriteFile(filepath.Join(capture, "extraction-result.json"), output, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.Login(ctx, analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key", BaseURL: "http://127.0.0.1:12345/v1", Model: "fixture-model"}); err != nil {
		t.Fatal(err)
	}
	if !s.acquire() {
		t.Fatal("main gate unexpectedly busy")
	}
	defer func() { <-s.gate }()
	result, err := s.Extract(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = extraction.Decode(result.JSON, input); err != nil {
		t.Fatal(err)
	}
	if result.Model != "fixture-model" || len(s.gate) != 1 || s.threadID != "" {
		t.Fatal("extraction changed the primary service")
	}
	raw, err := os.ReadFile(filepath.Join(capture, "turn.json"))
	if err != nil {
		t.Fatal(err)
	}
	var turn map[string]any
	_ = json.Unmarshal(raw, &turn)
	if turn["outputSchema"] == nil || len(turn["input"].([]any)) != 1 {
		t.Fatal("missing schema or review skill leaked into extraction")
	}
	raw, _ = os.ReadFile(filepath.Join(capture, "thread.json"))
	var thread map[string]any
	_ = json.Unmarshal(raw, &thread)
	if thread["dynamicTools"] != nil || !strings.Contains(thread["developerInstructions"].(string), "只返回") {
		t.Fatal("review tools/guidance used by extraction")
	}
	raw, _ = os.ReadFile(filepath.Join(capture, "args.json"))
	if !strings.Contains(string(raw), `web_search=\"disabled\"`) {
		t.Fatal("background web search enabled")
	}
}

func TestInstalledCodexExtractionSchema(t *testing.T) {
	if os.Getenv("RENT_CODEX_SMOKE") != "1" {
		t.Skip("set RENT_CODEX_SMOKE=1 for native CLI + local provider verification")
	}
	input, output := extractionFixture(t)
	captured := make(chan map[string]any, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		select {
		case captured <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(v any) { raw, _ := json.Marshal(v); fmt.Fprintf(w, "data: %s\n\n", raw); w.(http.Flusher).Flush() }
		item := map[string]any{"id": "msg_extract", "type": "message", "role": "assistant", "status": "completed", "phase": "final_answer", "content": []any{map[string]any{"type": "output_text", "text": string(output), "annotations": []any{}}}}
		emit(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_extract", "object": "response", "status": "in_progress", "output": []any{}}})
		emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_extract", "type": "message", "role": "assistant", "content": []any{}}})
		emit(map[string]any{"type": "response.output_text.delta", "item_id": "msg_extract", "output_index": 0, "content_index": 0, "delta": string(output)})
		emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		emit(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_extract", "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
	}))
	defer upstream.Close()
	s := New(Options{Home: t.TempDir()})
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := s.Login(ctx, analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-local-key", BaseURL: upstream.URL + "/v1", Model: "gpt-test-provider"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Extract(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = extraction.Decode(result.JSON, input); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-captured:
		encoded, _ := json.Marshal(body["text"])
		if !strings.Contains(string(encoded), "json_schema") || !strings.Contains(string(encoded), "monthly_rent") {
			t.Fatal("schema did not reach Responses API")
		}
		encoded, _ = json.Marshal(body["tools"])
		if strings.Contains(string(encoded), "web_search") || strings.Contains(string(encoded), "rental_calculate") {
			t.Fatal("review tools reached extraction")
		}
	default:
		t.Fatal("no native provider request")
	}
}
