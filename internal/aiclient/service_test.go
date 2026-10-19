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
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/document"
	"github.com/Felix-cy110/house-rent-table/internal/extraction"
)

func chatReply(w http.ResponseWriter, text, finish string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"model": "actual-model", "choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"role": "assistant", "content": text}}}})
}

func connectedService(t *testing.T, handler http.HandlerFunc, responses bool) (*Service, analysis.LoginRequest) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	input := analysis.LoginRequest{APIKey: "test-only-key", BaseURL: server.URL + "/v1", Model: "selected-model"}
	if responses {
		input.BaseURL += "/responses"
	}
	s := New(Options{ConfigDir: t.TempDir()})
	if _, err := s.Login(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	return s, input
}

func TestConnectionProbePersistenceRecoveryAndClear(t *testing.T) {
	var calls atomic.Int32
	s, input := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-only-key" {
			t.Error("wrong request endpoint or key")
		}
		var body struct {
			Model    string
			Messages []chatMessage
			Tools    []any
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "selected-model" || len(body.Tools) != 0 || len(body.Messages) != 2 || *body.Messages[1].Content != "Reply with OK." {
			t.Error("probe contains tools or wrong model/input")
		}
		chatReply(w, "OK", "stop")
	}, false)
	status, err := s.Account(context.Background())
	raw, _ := json.Marshal(status)
	if err != nil || !status.Connected || status.BaseURL != input.BaseURL || status.Model != input.Model || strings.Contains(string(raw), input.APIKey) {
		t.Fatalf("unsafe account: %s %v", raw, err)
	}
	if calls.Load() != 1 {
		t.Fatal("account should not invoke the provider")
	}
	path := filepath.Join(s.opts.ConfigDir, configFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restored := New(Options{ConfigDir: s.opts.ConfigDir})
	status, err = restored.Account(context.Background())
	if err != nil || !status.Connected {
		t.Fatal("configuration did not survive restart")
	}
	bad := input
	bad.APIKey, bad.Model = "wrong-key", "invalid-model"
	// Return a failed status for this candidate without touching the old service.
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"wrong-key and upstream-secret"}`))
	}))
	defer failed.Close()
	bad.BaseURL = failed.URL
	_, err = s.Login(context.Background(), bad)
	var connectionError *analysis.ConnectionError
	if !errors.As(err, &connectionError) || !strings.Contains(err.Error(), "Key 无效") || strings.Contains(err.Error(), "wrong-key") || strings.Contains(err.Error(), "upstream-secret") {
		t.Fatalf("unsafe probe failure: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed connection replaced saved configuration")
	}
	status, _ = s.Account(context.Background())
	if status.Model != input.Model || status.BaseURL != input.BaseURL {
		t.Fatal("failed connection changed live configuration")
	}
	if _, err := s.Login(context.Background(), input); err != nil {
		t.Fatalf("could not replace existing configuration: %v", err)
	}
	status, err = New(Options{ConfigDir: s.opts.ConfigDir}).Account(context.Background())
	if err != nil || !status.Connected || status.BaseURL != input.BaseURL || status.Model != input.Model {
		t.Fatal("replaced configuration did not survive restart")
	}
	if err := restored.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("clear left saved credentials")
	}
	status, err = New(Options{ConfigDir: s.opts.ConfigDir}).Account(context.Background())
	if err != nil || status.Connected {
		t.Fatal("cleared configuration restored after restart")
	}
}

func TestChatCalculationRoundTripAndUnchangedPayload(t *testing.T) {
	payload := json.RawMessage(" {\"fields\":[{\"label\":\"重复\",\"value\":\"0\",\"bold\":true},{\"label\":\"重复\",\"value\":null}]} \n")
	var rounds atomic.Int32
	s, _ := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string
			Messages []chatMessage
			Tools    []any
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
			return
		}
		if len(body.Tools) == 0 {
			chatReply(w, "OK", "stop")
			return
		}
		if len(body.Tools) != 1 || body.Model != "selected-model" || *body.Messages[1].Content != string(payload) || !strings.Contains(*body.Messages[0].Content, "当前接口未提供联网搜索") {
			t.Error("analysis lost input, rules, tools or model")
		}
		if rounds.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"calc-1","type":"function","function":{"name":"rental_calculate","arguments":"{\"calculations\":[{\"label\":\"押一付三\",\"expression\":\"2800*(1+3)+0.1+0.2\"}]}"}}]}}]}`))
			return
		}
		last := body.Messages[len(body.Messages)-1]
		if last.Role != "tool" || last.ToolCallID != "calc-1" || !strings.Contains(*last.Content, `"rounded":"11200.30"`) {
			t.Errorf("missing exact tool result: %+v", last)
		}
		chatReply(w, "  付款 11200.30 元\n\n未能核实市价。  ", "stop")
	}, false)
	result, err := s.Analyze(context.Background(), payload)
	if err != nil || result.Text != "  付款 11200.30 元\n\n未能核实市价。  " || rounds.Load() != 2 {
		t.Fatalf("analysis: %+v %v", result, err)
	}
}

func TestResponsesToolRoundTripAndSearch(t *testing.T) {
	var rounds atomic.Int32
	s, _ := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Error("wrong responses endpoint")
		}
		var body struct {
			Model, Instructions string
			Store               bool
			Input               []json.RawMessage
			Tools               []struct{ Type string }
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		if body.Store || body.Model != "selected-model" {
			t.Error("wrong model or storage")
		}
		if len(body.Tools) == 0 {
			_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`))
			return
		}
		if len(body.Tools) != 2 || body.Tools[0].Type != "web_search" || !strings.Contains(body.Instructions, "产品提示规则") {
			t.Error("missing search/function/rules")
		}
		if rounds.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"reasoning","id":"reasoning-1","summary":[]},{"type":"function_call","call_id":"calc-1","name":"rental_calculate","arguments":"{\"calculations\":[{\"label\":\"精度\",\"expression\":\"0.1+0.2\"}]}"}]}`))
			return
		}
		joined, _ := json.Marshal(body.Input)
		if !strings.Contains(string(joined), "reasoning-1") || !strings.Contains(string(joined), "function_call_output") || !strings.Contains(string(joined), "0.30") {
			t.Error("responses continuation lost tool result or reasoning")
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","status":"completed","content":[{"type":"output_text","text":"费用 0.30 元。"},{"type":"output_text","text":"参考价🏠[来源]","annotations":[{"type":"url_citation","title":"收费公示","url":"https://example.com/fees","start_index":4,"end_index":8},{"type":"url_citation","url":"https://example.com/invalid","start_index":-1,"end_index":90}]}]}]}`))
	}, true)
	result, err := s.Analyze(context.Background(), json.RawMessage(`{"fields":[]}`))
	if err != nil || result.Text != "费用 0.30 元。\n\n参考价🏠[来源]" || rounds.Load() != 2 {
		t.Fatalf("responses: %+v %v", result, err)
	}
	if len(result.Citations) != 1 || result.Citations[0].StartIndex != 16 || result.Citations[0].EndIndex != 20 || result.Citations[0].URL != "https://example.com/fees" {
		t.Fatalf("citation offsets lost across Unicode text parts: %+v", result.Citations)
	}
}

func TestExtractionUsesIndependentStructuredRequest(t *testing.T) {
	for _, responses := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "responses"}[responses], func(t *testing.T) {
			input := extraction.Input{Document: document.Document{SchemaVersion: 1, FileName: "test.xlsx", Fields: []document.Field{{ID: "f1", Label: "月租", Value: nil, Sheet: "S", Cell: "B2"}}}}
			s, _ := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				structured := len(body["response_format"]) != 0 || len(body["text"]) != 0
				if !structured {
					if responses {
						_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`))
					} else {
						chatReply(w, "OK", "stop")
					}
					return
				}
				if len(body["tools"]) != 0 {
					t.Error("extraction inherited review tools")
				}
				raw, _ := json.Marshal(body)
				if !strings.Contains(string(raw), "lease_months") || !strings.Contains(string(raw), "default_currency") || strings.Contains(string(raw), "本次租金溢价容忍值") {
					t.Error("wrong extraction schema/input/rules")
				}
				if responses {
					_, _ = w.Write([]byte(`{"model":"actual-extractor","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"version\":1}"}]}]}`))
				} else {
					chatReply(w, `{"version":1}`, "stop")
				}
			}, responses)
			// A report may own the gate while extraction proceeds independently.
			s.gate <- struct{}{}
			defer func() { <-s.gate }()
			out, err := s.Extract(context.Background(), input)
			if err != nil || string(out.JSON) != `{"version":1}` || out.Provider == "" || out.Model == "" {
				t.Fatalf("extraction: %+v %v", out, err)
			}
		})
	}
}

func TestIncompleteMalformedAndRefusedRepliesAreRejected(t *testing.T) {
	for name, response := range map[string]string{
		"truncated":        `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`,
		"empty":            `{"choices":[{"finish_reason":"stop","message":{"content":"  "}}]}`,
		"refused":          `{"choices":[{"finish_reason":"stop","message":{"content":"ignored","refusal":"no"}}]}`,
		"malformed":        `{"choices":`,
		"upstream-error":   `{"error":{"message":"test-only-key"}}`,
		"failed-responses": `{"status":"failed","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(response)) }))
			defer server.Close()
			base := server.URL
			if strings.Contains(name, "responses") {
				base += "/responses"
			}
			s := New(Options{ConfigDir: t.TempDir()})
			_, err := s.Login(context.Background(), analysis.LoginRequest{APIKey: "test-only-key", BaseURL: base, Model: "m"})
			if err == nil || strings.Contains(err.Error(), "test-only-key") {
				t.Fatalf("accepted incomplete/unsafe reply: %v", err)
			}
			if status, _ := s.Account(context.Background()); status.Connected {
				t.Fatal("failed probe was saved")
			}
		})
	}
}

func TestCancellationAndMutualExclusion(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	s, input := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Tools []any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) == 0 {
			chatReply(w, "OK", "stop")
			return
		}
		close(started)
		<-r.Context().Done()
		close(stopped)
	}, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Analyze(ctx, json.RawMessage(`{}`)); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("analysis did not start")
	}
	if _, err := s.Login(context.Background(), input); !errors.Is(err, analysis.ErrBusy) {
		t.Fatal("connection changed during analysis")
	}
	if err := s.Logout(context.Background()); !errors.Is(err, analysis.ErrBusy) {
		t.Fatal("configuration cleared during analysis")
	}
	if _, err := s.Analyze(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, analysis.ErrBusy) {
		t.Fatal("concurrent report accepted")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled analysis did not return")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request was not canceled")
	}
	if err := s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); chatReply(w, "OK", "stop") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	s := New(Options{ConfigDir: t.TempDir()})
	_, err := s.Login(context.Background(), analysis.LoginRequest{APIKey: "test-only-key", BaseURL: redirect.URL, Model: "m"})
	if err == nil || forwarded.Load() != 0 {
		t.Fatal("redirect forwarded credentials or accepted as connected")
	}
}

func TestMissingAndCorruptConfiguration(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{ConfigDir: dir})
	if _, err := s.Analyze(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, analysis.ErrLoginRequired) {
		t.Fatal("missing configuration did not reject analysis")
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(`{"apiKey":"secret-broken`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Options{ConfigDir: dir}).Account(context.Background())
	if !errors.Is(err, analysis.ErrUnavailable) || strings.Contains(err.Error(), "secret-broken") {
		t.Fatal("corrupt configuration leaked or was accepted")
	}
}
