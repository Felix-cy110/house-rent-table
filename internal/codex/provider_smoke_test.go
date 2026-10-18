package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

// Run the installed Codex against a local Responses fixture. No real key,
// model inference, user profile, or third-party endpoint is involved.
func TestInstalledCodexCustomProvider(t *testing.T) {
	if os.Getenv("RENT_CODEX_SMOKE") != "1" {
		t.Skip("set RENT_CODEX_SMOKE=1 to verify the installed CLI against a local provider")
	}
	for _, config := range []struct{ source, model string }{
		{"manual", "gpt-test-provider"}, {"ccswitch", "gpt-test-provider"},
		// This real model defaults to Responses Lite in the installed CLI. A
		// made-up model alone cannot catch missing hosted search/function tools.
		{"manual", "gpt-6-astra"}, {"ccswitch", "gpt-6-astra"},
	} {
		t.Run(config.source+"/"+config.model, func(t *testing.T) {
			type captured struct {
				Path, Authorization string
				Body                map[string]any
			}
			requests := make(chan captured, 8)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/responses") {
					http.NotFound(w, r)
					return
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					http.Error(w, "invalid request", 400)
					return
				}
				select {
				case requests <- captured{r.URL.Path, r.Header.Get("Authorization"), body}:
				default:
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(v any) {
					data, _ := json.Marshal(v)
					fmt.Fprintf(w, "data: %s\n\n", data)
					w.(http.Flusher).Flush()
				}
				input, _ := json.Marshal(body["input"])
				probe := strings.Contains(string(input), "Reply with OK.")
				calculated := strings.Contains(string(input), "function_call_output")
				item := map[string]any{"id": "msg_local", "type": "message", "role": "assistant", "status": "completed", "phase": "final_answer", "content": []any{map[string]any{"type": "output_text", "text": "本地供应商连接成功", "annotations": []any{}}}}
				if !probe && !calculated {
					item = map[string]any{"id": "fc_local", "type": "function_call", "status": "completed", "call_id": "call_local_calc", "name": "rental_calculate", "arguments": `{"calculations":[{"label":"首次付款","expression":"1500+1500+1500+300+100+100"}]}`}
				}
				emit(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_local", "object": "response", "status": "in_progress", "output": []any{}}})
				emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
				if item["type"] == "message" {
					emit(map[string]any{"type": "response.output_text.delta", "item_id": "msg_local", "output_index": 0, "content_index": 0, "delta": "本地供应商连接成功"})
				}
				emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
				emit(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_local", "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
			}))
			defer upstream.Close()
			s := New(Options{Home: t.TempDir()})
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			input := analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-local-provider-key", BaseURL: upstream.URL + "/v1", Model: config.model}
			if config.source == "ccswitch" {
				dir := t.TempDir()
				fixture(t, dir, "config.toml", fmt.Sprintf("model='%s'\nmodel_provider='custom'\n[model_providers.custom]\nbase_url='%s/v1'\nwire_api='responses'\nrequires_openai_auth=true\n", config.model, upstream.URL))
				fixture(t, dir, "auth.json", `{"OPENAI_API_KEY":"test-only-local-provider-key"}`)
				input = analysis.LoginRequest{Type: "apiKey", Source: "ccswitch", ConfigDir: dir}
			}
			if _, err := s.Login(ctx, input); err != nil {
				t.Fatal(err)
			}
			a, err := s.Account(ctx)
			if err != nil || !a.LoggedIn || a.AuthType != "apiKey" {
				t.Fatalf("custom provider readiness: %+v %v", a, err)
			}
			result, err := s.Analyze(ctx, json.RawMessage(`{"schemaVersion":1,"fileName":"test.xlsx","fields":[]}`))
			if err != nil || result.Text != "本地供应商连接成功" {
				t.Fatalf("local response was not delivered: %q %v", result.Text, err)
			}
			for stage := 0; stage < 3; stage++ {
				select {
				case req := <-requests:
					if req.Path != "/v1/responses" || req.Authorization != "Bearer test-only-local-provider-key" || req.Body["model"] != config.model {
						t.Fatal("wrong endpoint, credential or model reached local provider")
					}
					encoded, _ := json.Marshal(req.Body["tools"])
					input, _ := json.Marshal(req.Body["input"])
					if strings.Contains(string(input), "additional_tools") {
						t.Fatal("standard Responses provider received Responses Lite tool declarations")
					}
					if stage == 0 {
						if strings.Contains(string(encoded), "rental_calculate") || strings.Contains(string(encoded), "web_search") {
							t.Fatal("connection probe requested analysis tools")
						}
						if !strings.Contains(string(input), "Reply with OK.") || strings.Contains(string(input), "schemaVersion") {
							t.Fatal("connection probe did not use its minimal prompt")
						}
					} else if !strings.Contains(string(encoded), "rental_calculate") || !strings.Contains(string(encoded), "web_search") {
						t.Fatal("analysis tools were not sent to provider")
					}
					if stage == 2 && (!strings.Contains(string(input), "function_call_output") || !strings.Contains(string(input), "5000.00")) {
						t.Fatal("calculator result did not reach the provider")
					}
				default:
					t.Fatal("local provider received no request")
				}
			}
			if err := s.Logout(ctx); err != nil {
				t.Fatal(err)
			}
			a, err = s.Account(ctx)
			if err != nil || a.LoggedIn {
				t.Fatal("custom provider logout did not disconnect")
			}
		})
	}
}

func TestInstalledCodexRejectsInvalidProviderConnection(t *testing.T) {
	if os.Getenv("RENT_CODEX_SMOKE") != "1" {
		t.Skip("set RENT_CODEX_SMOKE=1 to verify failed connections against a local provider")
	}
	for _, tt := range []struct {
		status int
		want   string
	}{{401, "Key 无效"}, {403, "访问权限"}, {404, "地址或模型不存在"}} {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			requested := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case requested <- struct{}{}:
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, `{"error":{"message":"upstream-secret-must-not-leak","type":"invalid_request_error"}}`)
			}))
			defer upstream.Close()
			s := New(Options{Home: t.TempDir()})
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			_, err := s.Login(ctx, analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-local-provider-key", BaseURL: upstream.URL + "/v1", Model: "gpt-test-provider"})
			var connectionError *analysis.ConnectionError
			if !errors.As(err, &connectionError) || strings.Contains(err.Error(), "upstream-secret") {
				t.Fatalf("unexpected provider validation result: %v", err)
			}
			// Some CLI versions omit HTTP metadata. They must still reject the
			// connection with a useful local message without exposing upstream text.
			if !strings.Contains(err.Error(), tt.want) && err.Error() != probeCallError(analysis.ErrFailed).Error() {
				t.Fatalf("missing provider failure guidance: %v", err)
			}
			select {
			case <-requested:
			default:
				t.Fatal("connection was rejected without probing the provider")
			}
			if s.client != nil {
				t.Fatal("invalid provider became active")
			}
		})
	}
}
