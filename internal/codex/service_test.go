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
	"github.com/Felix-cy110/house-rent-table/internal/review"
	"github.com/Felix-cy110/house-rent-table/skills"
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
	customProvider := strings.Contains(strings.Join(os.Args, " "), "model_providers.rental_custom.requires_openai_auth=false")
	args, _ := json.Marshal(os.Args[1:])
	_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "args.json"), args, 0600)
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
			if customProvider && os.Getenv("RENT_CODEX_SCENARIO") == "init-fail" {
				_ = encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -1, "message": "test startup failure"}})
				continue
			}
			if customProvider {
				_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "provider-key"), []byte(os.Getenv(providerKeyEnv)), 0600)
			}
			_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "initialize.json"), req.Params, 0600)
			respond(map[string]string{"userAgent": "test"})
		case "initialized":
		case "account/read":
			var account any
			if loggedIn {
				account = map[string]string{"type": "apiKey"}
			}
			respond(map[string]any{"account": account, "requiresOpenaiAuth": !customProvider})
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
			_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "turn.json"), req.Params, 0600)
			var params struct {
				OutputSchema json.RawMessage `json:"outputSchema"`
				Input        []struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			_ = json.Unmarshal(req.Params, &params)
			_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "payload.json"), []byte(params.Input[0].Text), 0600)
			respond(map[string]any{"turn": map[string]string{"id": "turn-test"}})
			if len(params.OutputSchema) > 0 {
				output, _ := os.ReadFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "extraction-result.json"))
				notify("item/completed", map[string]any{"threadId": "thread-test", "turnId": "turn-test", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": string(output)}})
				notify("turn/completed", map[string]any{"threadId": "thread-test", "turn": map[string]string{"id": "turn-test", "status": "completed"}})
				continue
			}
			if os.Getenv("RENT_CODEX_SCENARIO") == "wait" {
				continue
			}
			// A server request must be answered while the turn is active, even
			// when unrelated notifications are interleaved on the same stream.
			_ = encoder.Encode(map[string]any{"id": "calc-1", "method": "item/tool/call", "params": map[string]any{
				"threadId": "thread-test", "turnId": "turn-test", "callId": "calc-1", "tool": review.ToolName,
				"arguments": map[string]any{"calculations": []review.Calculation{{Label: "首次付款", Expression: "3000+3000*3+300"}}},
			}})
			var reply json.RawMessage
			if decoder.Decode(&reply) != nil {
				return
			}
			_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "calculation.json"), reply, 0600)
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
	if _, err := s.Login(ctx, analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key"}); err != nil {
		t.Fatal(err)
	}
	payload := "{\n  \"schemaVersion\": 1, \"fileName\": \"测试.xlsx\", \"fields\": [\n" +
		`{"id":"f1","label":"未来项目","bold":true,"value":"  原文\n0 < > &","sheet":"S","cell":"B2"},` +
		`{"id":"f2","label":"未来项目","bold":false,"value":null,"sheet":"S","cell":"B3"}]}`
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
	if params["ephemeral"] != true || params["sandbox"] != "read-only" || !strings.Contains(params["developerInstructions"].(string), instructions) || len(params["dynamicTools"].([]any)) != 1 {
		t.Fatal("thread configuration missing")
	}
	turn, _ := os.ReadFile(filepath.Join(capture, "turn.json"))
	var turnParams struct {
		Input []struct{ Type, Name, Path string }
	}
	if json.Unmarshal(turn, &turnParams) != nil || len(turnParams.Input) != 2 || turnParams.Input[1].Type != "skill" || turnParams.Input[1].Name != skills.Name {
		t.Fatalf("missing explicit skill: %s", turn)
	}
	installed, err := os.ReadFile(turnParams.Input[1].Path)
	if err != nil || !strings.Contains(string(installed), "name: rental-review") {
		t.Fatal("skill not installed")
	}
	args, _ := os.ReadFile(filepath.Join(capture, "args.json"))
	var arguments []string
	_ = json.Unmarshal(args, &arguments)
	if !strings.Contains(strings.Join(arguments, " "), `web_search="live"`) {
		t.Fatal("live search disabled")
	}
	initData, _ := os.ReadFile(filepath.Join(capture, "initialize.json"))
	var initParams struct {
		Capabilities struct {
			Experimental bool `json:"experimentalApi"`
		}
	}
	_ = json.Unmarshal(initData, &initParams)
	if !initParams.Capabilities.Experimental {
		t.Fatal("dynamic tools require experimental capability")
	}
	calculated, _ := os.ReadFile(filepath.Join(capture, "calculation.json"))
	var reply struct {
		Result struct {
			Success      bool
			ContentItems []struct{ Text string }
		}
	}
	if json.Unmarshal(calculated, &reply) != nil || !reply.Result.Success || len(reply.Result.ContentItems) != 1 {
		t.Fatalf("tool reply: %s", calculated)
	}
	var calculation struct{ Calculations []review.CalculationResult }
	_ = json.Unmarshal([]byte(reply.Result.ContentItems[0].Text), &calculation)
	if len(calculation.Calculations) != 1 || calculation.Calculations[0].Rounded != "12300.00" {
		t.Fatalf("wrong calculation: %+v", calculation)
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
	login, err := s.Login(ctx, analysis.LoginRequest{Type: "chatgpt", APIKey: ""})
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
	_, _ = s.Login(ctx, analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key"})
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
	if _, err := s.Login(ctx, analysis.LoginRequest{Type: "chatgpt", APIKey: ""}); err != nil {
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
	_, _ = s.Login(ctx, analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key"})
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
	if _, err := s.Login(ctx, analysis.LoginRequest{Type: "apiKey", Source: "ccswitch", ConfigDir: t.TempDir()}); !errors.Is(err, analysis.ErrBusy) {
		t.Fatalf("provider changed during turn: %v", err)
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
	login, err := s.Login(ctx, analysis.LoginRequest{Type: "chatgpt", APIKey: ""})
	if err != nil || !strings.HasPrefix(login.AuthURL, "https://") {
		t.Fatalf("OAuth start failed: %v", err)
	}
	if err := s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = s.Login(ctx, analysis.LoginRequest{Type: "apiKey", APIKey: "sk-test-placeholder-not-a-real-key"})
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
	if err := c.call(ctx, "thread/start", s.threadParams(), &thread); err != nil {
		t.Fatalf("thread/start failed: %v", err)
	}
	if thread.Thread.ID == "" {
		t.Fatal("missing thread ID")
	}
	var listed struct {
		Data []struct {
			Skills []struct{ Name, Path string }
			Errors []json.RawMessage
		}
	}
	if err := c.call(ctx, "skills/list", map[string]any{"cwds": []string{s.workDir}, "forceReload": true}, &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range listed.Data {
		if len(entry.Errors) > 0 {
			t.Fatalf("skill errors: %s", entry.Errors)
		}
		for _, skill := range entry.Skills {
			if skill.Name == skills.Name && filepath.Clean(skill.Path) == filepath.Clean(s.skillPath) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("bundled skill not discovered: %+v", listed)
	}
	if err := c.call(ctx, "thread/unsubscribe", map[string]string{"threadId": thread.Thread.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("Installed Codex initialize / OAuth start-cancel / API key registration-logout / dynamic-tool thread / skill discovery passed; no inference performed")
}
