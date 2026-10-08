package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

// This test executable also acts as a deterministic stdio protocol peer. No
// real credentials or inference are used by the ordinary test suite.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "app-server" {
		fakeServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeServer() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	loggedIn := false
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if decoder.Decode(&req) != nil {
			return
		}
		respond := func(value any) { _ = encoder.Encode(map[string]any{"id": req.ID, "result": value}) }
		notify := func(method string, params any) {
			_ = encoder.Encode(map[string]any{"method": method, "params": params})
		}
		switch req.Method {
		case "initialize":
			respond(map[string]string{"userAgent": "test"})
		case "initialized":
		case "account/read":
			var account any
			if loggedIn {
				account = map[string]string{"type": "apiKey"}
			}
			respond(map[string]any{"account": account, "requiresOpenaiAuth": true})
		case "account/login/start":
			var params struct {
				Type   string `json:"type"`
				APIKey string `json:"apiKey"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.Type == "chatgpt" {
				if os.Getenv("RENT_CODEX_SCENARIO") == "oauth-fail-early" {
					notify("account/login/completed", map[string]any{"loginId": "test-login", "success": false})
				}
				respond(map[string]string{"type": "chatgpt", "authUrl": "https://auth.openai.com/test", "loginId": "test-login"})
			} else {
				loggedIn = params.APIKey == "test-only-key"
				respond(map[string]string{"type": "apiKey"})
				notify("account/login/completed", map[string]any{"loginId": nil, "success": loggedIn})
			}
		case "account/logout", "account/login/cancel":
			loggedIn = false
			respond(map[string]any{})
		case "thread/start":
			_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "thread.json"), req.Params, 0600)
			respond(map[string]any{"thread": map[string]string{"id": "thread-test"}})
		case "turn/start":
			var params struct {
				Input []struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			_ = json.Unmarshal(req.Params, &params)
			_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "payload.json"), []byte(params.Input[0].Text), 0600)
			respond(map[string]any{"turn": map[string]string{"id": "turn-test"}})
			if os.Getenv("RENT_CODEX_SCENARIO") == "wait" {
				continue
			}
			// Other threads and commentary must not become the final response.
			notify("item/completed", map[string]any{"threadId": "other", "turnId": "turn-test", "item": map[string]string{"type": "agentMessage", "text": "别的表格"}})
			notify("item/completed", map[string]any{"threadId": "thread-test", "turnId": "turn-test", "item": map[string]string{"type": "agentMessage", "phase": "commentary", "text": "处理中"}})
			notify("item/completed", map[string]any{"threadId": "thread-test", "turnId": "turn-test", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "原始回复\n  保留空格 <script>"}})
			status := "completed"
			if os.Getenv("RENT_CODEX_SCENARIO") == "fail" {
				status = "failed"
			}
			notify("turn/completed", map[string]any{"threadId": "thread-test", "turn": map[string]string{"id": "turn-test", "status": status}})
		case "turn/interrupt":
			_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "interrupted"), []byte("yes"), 0600)
			respond(map[string]any{})
		case "thread/unsubscribe":
			respond(map[string]any{})
		default:
			_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32601, "message": "unexpected method"}})
		}
	}
}

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	capture := t.TempDir()
	t.Setenv("RENT_CODEX_CAPTURE", capture)
	s := New(Options{Binary: executable, Home: t.TempDir()})
	t.Cleanup(s.Close)
	return s, capture
}

func TestProtocolLoginForwardAndLogout(t *testing.T) {
	s, capture := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Analyze(ctx, json.RawMessage(`{}`)); !errors.Is(err, analysis.ErrLoginRequired) {
		t.Fatalf("expected login requirement: %v", err)
	}
	if _, err := s.Login(ctx, "apiKey", "test-only-key"); err != nil {
		t.Fatal(err)
	}
	payload := "{\n  \"schemaVersion\": 1, \"fileName\": \"测试.xlsx\", \"fields\": [\n" +
		`{"id":"f1","label":"未来项目","value":"  原文\n0 < > &","sheet":"S","cell":"B2"},` +
		`{"id":"f2","label":"未来项目","value":null,"sheet":"S","cell":"B3"}]}`
	result, err := s.Analyze(ctx, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	sent, err := os.ReadFile(filepath.Join(capture, "payload.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sent) != payload {
		t.Fatalf("input was rewritten: %s", sent)
	}
	if result.Text != "原始回复\n  保留空格 <script>" {
		t.Fatalf("output was rewritten: %q", result.Text)
	}
	thread, _ := os.ReadFile(filepath.Join(capture, "thread.json"))
	var params map[string]any
	_ = json.Unmarshal(thread, &params)
	if params["ephemeral"] != true || params["sandbox"] != "read-only" || params["developerInstructions"] != instructions {
		t.Fatal("thread configuration missing")
	}
	if err := s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := s.Account(ctx)
	if err != nil || a.LoggedIn {
		t.Fatalf("logout failed: %+v %v", a, err)
	}
}

func TestOAuthCanBeCanceled(t *testing.T) {
	s, _ := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	login, err := s.Login(ctx, "chatgpt", "")
	if err != nil || login.AuthURL != "https://auth.openai.com/test" {
		t.Fatalf("login: %+v %v", login, err)
	}
	a, err := s.Account(ctx)
	if err != nil || !a.Pending || a.LoggedIn {
		t.Fatalf("account: %+v %v", a, err)
	}
	if err := s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Account(ctx)
	if a.Pending {
		t.Fatal("login still pending")
	}
}

func TestFailedTurnDoesNotReturnPartialSuccess(t *testing.T) {
	t.Setenv("RENT_CODEX_SCENARIO", "fail")
	s, _ := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.Login(ctx, "apiKey", "test-only-key")
	result, err := s.Analyze(ctx, json.RawMessage(`{}`))
	if !errors.Is(err, analysis.ErrFailed) || result.Text != "" {
		t.Fatalf("failed turn accepted: %+v %v", result, err)
	}
}

func TestOAuthCompletionBeforeResponseIsNotLost(t *testing.T) {
	t.Setenv("RENT_CODEX_SCENARIO", "oauth-fail-early")
	s, _ := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Login(ctx, "chatgpt", ""); err != nil {
		t.Fatal(err)
	}
	a, err := s.Account(ctx)
	if err != nil || a.Pending || a.Error == "" {
		t.Fatalf("early completion lost: %+v %v", a, err)
	}
}

func TestCancellationInterruptsTurnAndRejectsConcurrentWork(t *testing.T) {
	t.Setenv("RENT_CODEX_SCENARIO", "wait")
	s, capture := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.Login(ctx, "apiKey", "test-only-key")
	run, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() { _, err := s.Analyze(run, json.RawMessage(`{}`)); finished <- err }()
	for {
		if _, err := os.Stat(filepath.Join(capture, "payload.json")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("turn did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := s.Logout(ctx); !errors.Is(err, analysis.ErrBusy) {
		t.Fatalf("account changed during turn: %v", err)
	}
	stop()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(capture, "interrupted")); err != nil {
		t.Fatal("turn was not interrupted")
	}
}

func TestMissingExecutable(t *testing.T) {
	s := New(Options{Binary: filepath.Join(t.TempDir(), "missing-codex"), Home: t.TempDir()})
	defer s.Close()
	if _, err := s.Account(context.Background()); !errors.Is(err, analysis.ErrUnavailable) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Explicit opt-in smoke test against an installed CLI. It does not run inference
// or touch the user's Codex profile; the placeholder key stays in a temp profile.
func TestInstalledCodexProtocol(t *testing.T) {
	if os.Getenv("RENT_CODEX_SMOKE") != "1" {
		t.Skip("set RENT_CODEX_SMOKE=1 to verify the installed CLI")
	}
	s := New(Options{Home: t.TempDir()})
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := s.Account(ctx)
	if err != nil || a.LoggedIn {
		t.Fatalf("initial account: %+v %v", a, err)
	}
	login, err := s.Login(ctx, "chatgpt", "")
	if err != nil || !strings.HasPrefix(login.AuthURL, "https://") {
		t.Fatalf("OAuth start failed: %v", err)
	}
	if err := s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = s.Login(ctx, "apiKey", "sk-test-placeholder-not-a-real-key")
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.Account(ctx)
	if err != nil || !a.LoggedIn || a.AuthType != "apiKey" {
		t.Fatalf("API key account: %+v %v", a, err)
	}
	c, err := s.connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := c.call(ctx, "thread/start", map[string]any{"cwd": s.workDir, "ephemeral": true, "sandbox": "read-only", "approvalPolicy": "never", "developerInstructions": instructions}, &thread); err != nil {
		t.Fatalf("thread/start failed: %v", err)
	}
	if thread.Thread.ID == "" {
		t.Fatal("missing thread ID")
	}
	if err := c.call(ctx, "thread/unsubscribe", map[string]string{"threadId": thread.Thread.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("Installed Codex initialize / OAuth start-cancel / API key registration-logout / ephemeral thread passed; no inference performed")
}
