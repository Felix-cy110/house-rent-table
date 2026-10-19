package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clientFor(server *httptest.Server) *http.Client {
	target, _ := url.Parse(server.URL)
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		copyURL := *r.URL
		copyURL.Scheme = target.Scheme
		copyURL.Host = target.Host
		clone.URL = &copyURL
		return http.DefaultTransport.RoundTrip(clone)
	})}
}
func connectedService(t *testing.T, handler http.HandlerFunc, provider string) (*Service, analysis.LoginRequest) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	input := analysis.LoginRequest{APIKey: "test-only-key", Provider: provider}
	s := New(Options{ConfigDir: t.TempDir(), HTTPClient: clientFor(server)})
	if _, err := s.Login(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	return s, input
}

func TestConnectionProbePersistenceRecoveryAndClear(t *testing.T) {
	var calls atomic.Int32
	s, input := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer wrong-key" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"wrong-key and upstream-secret"}`))
			return
		}
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-only-key" {
			t.Error("wrong request endpoint or key")
		}
		var body struct {
			Model    string
			Messages []chatMessage
			Tools    []any
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "mimo-v2.6-flash" || len(body.Tools) != 0 || len(body.Messages) != 2 || *body.Messages[1].Content != "Reply with OK." {
			t.Error("probe contains tools or wrong model/input")
		}
		chatReply(w, "OK", "stop")
	}, "mimo")
	status, err := s.Account(context.Background())
	raw, _ := json.Marshal(status)
	if err != nil || !status.Connected || status.Provider != input.Provider || status.Model != "mimo-v2.6-flash" || strings.Contains(string(raw), input.APIKey) {
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
	bad.APIKey = "wrong-key"
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
	if status.Model != "mimo-v2.6-flash" || status.Provider != input.Provider {
		t.Fatal("failed connection changed live configuration")
	}
	if _, err := s.Login(context.Background(), input); err != nil {
		t.Fatalf("could not replace existing configuration: %v", err)
	}
	status, err = New(Options{ConfigDir: s.opts.ConfigDir}).Account(context.Background())
	if err != nil || !status.Connected || status.Provider != input.Provider || status.Model != "mimo-v2.6-flash" {
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
		if len(body.Tools) != 2 || body.Model != "mimo-v2.6-flash" || *body.Messages[1].Content != string(payload) || !strings.Contains(*body.Messages[0].Content, "联网核价必须使用") {
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
	}, "mimo")
	result, err := s.Analyze(context.Background(), payload)
	if err != nil || result.Text != "  付款 11200.30 元\n\n未能核实市价。  " || rounds.Load() != 2 {
		t.Fatalf("analysis: %+v %v", result, err)
	}
}

func TestExtractionUsesIndependentStructuredRequest(t *testing.T) {
	for _, provider := range modelconfig.All() {
		t.Run(provider.ID, func(t *testing.T) {
			input := extraction.Input{Document: document.Document{SchemaVersion: 1, FileName: "test.xlsx", Fields: []document.Field{{ID: "f1", Label: "月租", Sheet: "S", Cell: "B2"}}}}
			s, _ := connectedService(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				assertThinking(t, provider.ID, body)
				if len(body["response_format"]) == 0 {
					chatReply(w, "OK", "stop")
					return
				}
				if len(body["tools"]) != 0 || !strings.Contains(string(body["response_format"]), "json_object") {
					t.Error("unexpected extraction format/tools")
				}
				if !strings.Contains(string(body["messages"]), "lease_months") || !strings.Contains(string(body["messages"]), "default_currency") || strings.Contains(string(body["messages"]), "本次租金溢价容忍值") {
					t.Error("lost schema, input or independent instructions")
				}
				chatReply(w, `{"version":1}`, "stop")
			}, provider.ID)
			s.gate <- struct{}{}
			defer func() { <-s.gate }()
			out, err := s.Extract(context.Background(), input)
			if err != nil || string(out.JSON) != `{"version":1}` || out.Provider != provider.ID || out.Model != "actual-model" {
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
			s := New(Options{ConfigDir: t.TempDir(), HTTPClient: clientFor(server)})
			_, err := s.Login(context.Background(), analysis.LoginRequest{APIKey: "test-only-key", Provider: "mimo"})
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
	}, "mimo")
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
	s := New(Options{ConfigDir: t.TempDir(), HTTPClient: clientFor(redirect)})
	_, err := s.Login(context.Background(), analysis.LoginRequest{APIKey: "test-only-key", Provider: "glm"})
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
