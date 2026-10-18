package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

func fakeProbe(encoder *json.Encoder, id, params json.RawMessage) bool {
	_ = os.WriteFile(filepath.Join(os.Getenv("RENT_CODEX_CAPTURE"), "probe-turn.json"), params, 0600)
	notify := func(method string, params any) {
		_ = encoder.Encode(map[string]any{"method": method, "params": params})
	}
	scenario := os.Getenv("RENT_CODEX_SCENARIO")
	if os.Getenv(providerKeyEnv) == "wrong-key" {
		scenario = "probe-unauthorized"
	}
	if scenario == "probe-rejected" {
		_ = encoder.Encode(map[string]any{"id": id, "error": map[string]any{"code": -1, "message": "upstream-secret-must-not-leak"}})
		return false
	}
	_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"turn": map[string]string{"id": "turn-probe"}}})
	if scenario == "probe-exit" {
		return true
	}
	if scenario == "probe-wait" {
		return false
	}
	info := any(nil)
	status, phase, text := "completed", "final_answer", "OK"
	switch scenario {
	case "probe-unauthorized":
		info, status = "unauthorized", "failed"
	case "probe-model":
		info, status = map[string]any{"httpConnectionFailed": map[string]int{"httpStatusCode": 404}}, "failed"
	case "probe-quota":
		info, status = "usageLimitExceeded", "failed"
	case "probe-partial":
		status = "failed"
	case "probe-empty":
		text = ""
	case "probe-whitespace":
		text = " \n "
	case "probe-commentary":
		phase = "commentary"
	case "probe-foreign":
		notify("item/completed", map[string]any{"threadId": "other", "turnId": "turn-probe", "item": map[string]string{"type": "agentMessage", "text": "OK"}})
		notify("item/completed", map[string]any{"threadId": "thread-probe", "turnId": "other", "item": map[string]string{"type": "agentMessage", "text": "OK"}})
		text = ""
	case "probe-error-event", "probe-retry":
		notify("error", map[string]any{"threadId": "thread-probe", "turnId": "turn-probe", "willRetry": scenario == "probe-retry", "error": map[string]any{"message": "upstream-secret-must-not-leak", "codexErrorInfo": "unauthorized"}})
		if scenario == "probe-error-event" {
			status = "failed"
		}
	}
	if text != "" {
		notify("item/completed", map[string]any{"threadId": "thread-probe", "turnId": "turn-probe", "item": map[string]string{"type": "agentMessage", "phase": phase, "text": text}})
	}
	notify("turn/completed", map[string]any{"threadId": "thread-probe", "turn": map[string]any{"id": "turn-probe", "status": status, "error": map[string]any{"message": "upstream-secret-must-not-leak", "codexErrorInfo": info}}})
	return false
}

func TestConnectionProbeUsesSelectedModelWithoutAnalysisDataOrTools(t *testing.T) {
	for _, source := range []string{"manual", "openai", "ccswitch", "retry"} {
		t.Run(source, func(t *testing.T) {
			s, capture := testService(t)
			s.opts.Model = "startup-model"
			input := analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key", BaseURL: "https://gateway.example/v1", Model: "selected-model"}
			wantModel := input.Model
			switch source {
			case "openai":
				input.BaseURL, input.Model, wantModel = "", "", s.opts.Model
			case "ccswitch":
				dir := t.TempDir()
				fixture(t, dir, "config.toml", "model='selected-model'\nmodel_provider='p'\n[model_providers.p]\nbase_url='https://gateway.example/v1'\nexperimental_bearer_token='test-only-key'\n")
				input = analysis.LoginRequest{Type: "apiKey", Source: "ccswitch", ConfigDir: dir}
			case "retry":
				t.Setenv("RENT_CODEX_SCENARIO", "probe-retry")
			}
			if _, err := s.Login(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			var thread struct {
				Model, Sandbox, ApprovalPolicy, BaseInstructions string
				Ephemeral                                        bool
				DynamicTools                                     []any
				Config                                           map[string]any
			}
			data, err := os.ReadFile(filepath.Join(capture, "probe-thread.json"))
			if err != nil || json.Unmarshal(data, &thread) != nil || thread.Model != wantModel || !thread.Ephemeral || thread.Sandbox != "read-only" || thread.ApprovalPolicy != "never" || thread.BaseInstructions != probeInstructions || len(thread.DynamicTools) != 0 || thread.Config["web_search"] != "disabled" {
				t.Fatalf("probe configuration: %s %v", data, err)
			}
			data, _ = os.ReadFile(filepath.Join(capture, "probe-turn.json"))
			var turn struct{ Input []struct{ Type, Text string } }
			if json.Unmarshal(data, &turn) != nil || len(turn.Input) != 1 || turn.Input[0].Type != "text" || turn.Input[0].Text != "Reply with OK." || strings.Contains(string(data), "test-only-key") {
				t.Fatal("probe leaked analysis inputs or credentials")
			}
			account, err := s.Account(context.Background())
			if err != nil || !account.LoggedIn {
				t.Fatalf("successful probe did not connect: %+v %v", account, err)
			}
			if s.events != nil || s.threadID != "" {
				t.Fatal("probe subscription remained active")
			}
		})
	}
}

func TestConnectionProbeRejectsFailedOrInvalidReplies(t *testing.T) {
	for scenario, want := range map[string]string{
		"unauthorized": "Key 无效", "model": "地址或模型不存在", "quota": "额度不足",
		"partial": "未返回有效回复", "empty": "未返回有效回复", "whitespace": "未返回有效回复",
		"commentary": "未返回有效回复", "foreign": "未返回有效回复", "rejected": "未返回有效回复",
		"exit": "验证中断", "error-event": "Key 无效",
	} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := testService(t)
			t.Setenv("RENT_CODEX_SCENARIO", "probe-"+scenario)
			_, err := s.Login(context.Background(), analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key", BaseURL: "https://gateway.example/v1", Model: "m"})
			var connectionError *analysis.ConnectionError
			if !errors.As(err, &connectionError) || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "upstream-secret") || s.client != nil {
				t.Fatalf("failed probe accepted or unsafe error: %v", err)
			}
			if provider, loadErr := loadProvider(s.opts.Home); loadErr != nil || provider != nil {
				t.Fatal("failed probe persisted configuration")
			}
		})
	}
}

func TestFailedConnectionProbePreservesExistingConnection(t *testing.T) {
	for _, original := range []string{"manual", "openai"} {
		for _, replacement := range []string{"manual", "openai", "ccswitch"} {
			t.Run(original+"-to-"+replacement, func(t *testing.T) {
				s, _ := testService(t)
				input := analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key"}
				if original == "manual" {
					input.BaseURL, input.Model = "https://gateway.example/v1", "original-model"
				}
				if _, err := s.Login(context.Background(), input); err != nil {
					t.Fatal(err)
				}
				previous := s.client
				before, err := s.Account(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				snapshot, _ := os.ReadFile(filepath.Join(s.opts.Home, providerFile))
				input.APIKey = "wrong-key"
				switch replacement {
				case "manual":
					input.BaseURL, input.Model = "https://replacement.example/v1", "new-model"
				case "openai":
					input.BaseURL, input.Model = "", ""
				case "ccswitch":
					dir := t.TempDir()
					fixture(t, dir, "config.toml", "model='new-model'\nmodel_provider='p'\n[model_providers.p]\nbase_url='https://replacement.example/v1'\nexperimental_bearer_token='wrong-key'\n")
					input = analysis.LoginRequest{Type: "apiKey", Source: "ccswitch", ConfigDir: dir}
				}
				if _, err := s.Login(context.Background(), input); err == nil {
					t.Fatal("bad key accepted")
				}
				after, err := s.Account(context.Background())
				stored, _ := os.ReadFile(filepath.Join(s.opts.Home, providerFile))
				if err != nil || before != after || s.client != previous || isDone(previous) || string(stored) != string(snapshot) {
					t.Fatal("failed probe damaged existing connection or saved configuration")
				}
				if _, err := s.Analyze(context.Background(), json.RawMessage(`{}`)); err != nil {
					t.Fatalf("previous connection unusable: %v", err)
				}
			})
		}
	}
}

func TestConnectionProbeTimeoutCleansUpAndAllowsRetry(t *testing.T) {
	s, capture := testService(t)
	t.Setenv("RENT_CODEX_SCENARIO", "probe-wait")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan error, 1)
	input := analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key", BaseURL: "https://gateway.example/v1", Model: "m"}
	go func() { _, err := s.Login(ctx, input); finished <- err }()
	for {
		if _, err := os.Stat(filepath.Join(capture, "probe-turn.json")); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("probe finished before requesting model: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := s.Login(context.Background(), input); !errors.Is(err, analysis.ErrBusy) {
		t.Fatalf("duplicate connection probe accepted: %v", err)
	}
	if _, err := s.Analyze(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, analysis.ErrBusy) {
		t.Fatalf("analysis accepted during connection probe: %v", err)
	}
	err := <-finished
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "验证超时") {
		t.Fatalf("timeout not reported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(capture, "interrupted")); err != nil {
		t.Fatal("timed out probe was not interrupted")
	}
	data, _ := os.ReadFile(filepath.Join(capture, "unsubscribed.json"))
	if !strings.Contains(string(data), "thread-probe") || s.events != nil || s.threadID != "" {
		t.Fatal("timed out probe did not unsubscribe")
	}
	t.Setenv("RENT_CODEX_SCENARIO", "")
	if _, err := s.Login(context.Background(), input); err != nil {
		t.Fatalf("could not retry after timeout: %v", err)
	}
}
