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

func fixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestImportCCSwitchSelectsActiveProviderAndCredentials(t *testing.T) {
	for _, mode := range []string{"auth-file", "inline-token", "environment", "profile", "local-route"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			config := "model = 'test-model'\nmodel_provider = 'selected'\n"
			provider := "[model_providers.selected]\nname = '测试供应商'\nbase_url = 'https://gateway.example/v1'\nwire_api = 'responses'\n"
			switch mode {
			case "auth-file":
				provider += "requires_openai_auth = true\n"
				fixture(t, dir, "auth.json", `{"OPENAI_API_KEY":"test-only-key","auth_mode":"apikey"}`)
			case "inline-token":
				provider += "experimental_bearer_token = 'test-only-key'\n"
				// Preserved official OAuth material must not override the provider token.
				fixture(t, dir, "auth.json", `{"OPENAI_API_KEY":null,"auth_mode":"chatgpt","tokens":{"access_token":"unrelated-oauth-secret"}}`)
			case "environment":
				provider += "env_key = 'TEST_PROVIDER_KEY'\n"
				t.Setenv("TEST_PROVIDER_KEY", "test-only-key")
			case "profile":
				config = "model = 'wrong-model'\nmodel_provider = 'inactive'\nprofile = 'selected-profile'\n[profiles.selected-profile]\nmodel = 'test-model'\nmodel_provider = 'selected'\n"
				provider += "experimental_bearer_token = 'test-only-key'\n"
			case "local-route":
				provider = "[model_providers.selected]\nname = '测试供应商'\nbase_url = 'http://127.0.0.1:15721/v1'\nwire_api = 'responses'\nrequires_openai_auth = true\n"
				fixture(t, dir, "auth.json", `{"OPENAI_API_KEY":"test-only-key"}`)
			}
			config += provider + "[model_providers.inactive]\nbase_url = 'https://wrong.example/v1'\nexperimental_bearer_token = 'wrong-secret'\n[mcp_servers.unrelated]\ncommand = 'do-not-copy'\n"
			fixture(t, dir, "config.toml", config)
			p, err := importCCSwitch(dir, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if p.APIKey != "test-only-key" || p.Model != "test-model" || p.Name != "测试供应商" || p.Source != "ccswitch" || strings.Contains(p.BaseURL, "wrong") {
				t.Fatal("wrong active provider imported")
			}
			out, _ := json.Marshal(p)
			for _, unwanted := range []string{"unrelated-oauth-secret", "wrong-secret", "mcp_servers", "do-not-copy"} {
				if strings.Contains(string(out), unwanted) {
					t.Fatal("unrelated configuration was copied")
				}
			}
			unchanged, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
			if string(unchanged) != config {
				t.Fatal("source configuration changed")
			}
		})
	}
}

func TestImportCCSwitchRejectsIncompleteAndUnsupportedConfigurations(t *testing.T) {
	for name, config := range map[string]string{
		"malformed":        "model = secret-that-must-not-leak",
		"missing-active":   "model='m'\nmodel_provider='absent'\n[model_providers.other]\nbase_url='https://wrong.example'\n",
		"missing-model":    "model_provider='p'\n[model_providers.p]\nbase_url='https://example.com'\n",
		"oauth-only":       "model='m'\n",
		"unsupported-wire": "model='m'\nmodel_provider='p'\n[model_providers.p]\nbase_url='https://example.com'\nwire_api='chat'\n",
		"missing-env":      "model='m'\nmodel_provider='p'\n[model_providers.p]\nbase_url='https://example.com'\nenv_key='TEST_MISSING_PROVIDER_KEY'\n",
		"query-secret":     "model='m'\nmodel_provider='p'\n[model_providers.p]\nbase_url='https://example.com?key=secret-that-must-not-leak'\nexperimental_bearer_token='test-key'\n",
		"custom-headers":   "model='m'\nmodel_provider='p'\n[model_providers.p]\nbase_url='https://example.com'\nhttp_headers={ Authorization='secret-that-must-not-leak' }\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TEST_MISSING_PROVIDER_KEY", "")
			dir := t.TempDir()
			fixture(t, dir, "config.toml", config)
			fixture(t, dir, "auth.json", `{"auth_mode":"chatgpt","tokens":{"access_token":"secret-that-must-not-leak"}}`)
			_, err := importCCSwitch(dir, t.TempDir())
			var configErr *analysis.ConfigError
			if !errors.As(err, &configErr) || strings.Contains(err.Error(), "secret-that-must-not-leak") {
				t.Fatalf("expected safe configuration error: %v", err)
			}
		})
	}
}

func TestImportDirectoryResolution(t *testing.T) {
	user := t.TempDir()
	dir, err := importDir("", user)
	if err != nil || dir != filepath.Join(user, ".codex") {
		t.Fatal("wrong default directory")
	}
	fixture(t, filepath.Join(user, ".cc-switch"), "settings.json", `{"codexConfigDir":"~/custom-codex"}`)
	dir, err = importDir("", user)
	if err != nil || dir != filepath.Join(user, "custom-codex") {
		t.Fatal("settings override ignored")
	}
	explicit := t.TempDir()
	dir, err = importDir(explicit, user)
	if err != nil || dir != explicit {
		t.Fatal("explicit directory ignored")
	}
	if _, err := importDir("relative-directory", user); err == nil {
		t.Fatal("relative directory accepted")
	}
}

func TestCustomProviderLifecycleAndSnapshot(t *testing.T) {
	s, capture := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	input := analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key", BaseURL: "https://gateway.example/v1", Model: "chosen-model"}
	if _, err := s.Login(ctx, input); err != nil {
		t.Fatal(err)
	}
	a, err := s.Account(ctx)
	if err != nil || !a.LoggedIn || a.AuthType != "apiKey" || a.Model != "chosen-model" {
		t.Fatalf("provider not ready: %+v %v", a, err)
	}
	key, _ := os.ReadFile(filepath.Join(capture, "provider-key"))
	args, _ := os.ReadFile(filepath.Join(capture, "args.json"))
	account, _ := json.Marshal(a)
	if string(key) != input.APIKey || strings.Contains(string(args), input.APIKey) || strings.Contains(string(account), input.APIKey) {
		t.Fatal("key not isolated to child environment/private storage")
	}
	s.opts.Model = "startup-model-must-not-override-selection"
	if _, err := s.Analyze(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	var thread map[string]any
	data, _ := os.ReadFile(filepath.Join(capture, "thread.json"))
	_ = json.Unmarshal(data, &thread)
	if thread["model"] != "chosen-model" {
		t.Fatal("selected model not used for analysis")
	}
	s.Close()
	restarted := New(s.opts)
	t.Cleanup(restarted.Close)
	a, err = restarted.Account(ctx)
	if err != nil || !a.LoggedIn || a.Model != "chosen-model" {
		t.Fatal("provider did not survive application restart")
	}
	if err := restarted.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	a, err = restarted.Account(ctx)
	if err != nil || a.LoggedIn {
		t.Fatal("logout left the provider connected")
	}
	data, _ = os.ReadFile(filepath.Join(s.opts.Home, providerFile))
	if strings.Contains(string(data), input.APIKey) {
		t.Fatal("logout retained provider key")
	}
}

func TestProviderFailureKeepsPreviousConnectionAndOAuthCanBeRestored(t *testing.T) {
	s, _ := testService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	input := analysis.LoginRequest{Type: "apiKey", APIKey: "test-only-key", BaseURL: "https://gateway.example/v1", Model: "original-model"}
	if _, err := s.Login(ctx, input); err != nil {
		t.Fatal(err)
	}
	previous := s.client
	t.Setenv("RENT_CODEX_SCENARIO", "init-fail")
	input.Model = "replacement-model"
	if _, err := s.Login(ctx, input); err == nil {
		t.Fatal("startup failure accepted")
	}
	snapshot, err := loadProvider(s.opts.Home)
	if err != nil || snapshot.Model != "original-model" || s.client != previous || isDone(previous) {
		t.Fatal("failed switch damaged previous connection")
	}
	if _, err := s.Login(ctx, analysis.LoginRequest{Type: "apiKey", Source: "ccswitch", ConfigDir: t.TempDir()}); err == nil || s.client != previous {
		t.Fatal("failed import damaged previous connection")
	}
	login, err := s.Login(ctx, analysis.LoginRequest{Type: "chatgpt"})
	if err != nil || login.AuthURL == "" || s.client.provider != nil {
		t.Fatal("OAuth could not be restored")
	}
	snapshot, err = loadProvider(s.opts.Home)
	if err != nil || snapshot != nil {
		t.Fatal("OAuth retained custom provider routing")
	}
}

func TestImportedConfigurationRemainsUnchangedAfterLogout(t *testing.T) {
	s, _ := testService(t)
	dir := t.TempDir()
	config := "model='test-model'\nmodel_provider='custom'\n[model_providers.custom]\nbase_url='http://127.0.0.1:15721/v1'\nrequires_openai_auth=true\n"
	auth := `{"OPENAI_API_KEY":"test-only-key"}`
	fixture(t, dir, "config.toml", config)
	fixture(t, dir, "auth.json", auth)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Login(ctx, analysis.LoginRequest{Type: "apiKey", Source: "ccswitch", ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"config.toml": config, "auth.json": auth} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want {
			t.Fatal("import/logout changed source credentials")
		}
	}
}

func TestImportRejectsSharedConfigurationDirectory(t *testing.T) {
	s, _ := testService(t)
	fixture(t, s.opts.Home, "config.toml", "model='m'\nmodel_provider='custom'\n[model_providers.custom]\nbase_url='https://example.com/v1'\n")
	auth := `{"OPENAI_API_KEY":"test-only-key"}`
	fixture(t, s.opts.Home, "auth.json", auth)
	_, err := s.Login(context.Background(), analysis.LoginRequest{Type: "apiKey", Source: "ccswitch", ConfigDir: s.opts.Home})
	var configError *analysis.ConfigError
	if !errors.As(err, &configError) {
		t.Fatal("source and target profile were allowed to overlap")
	}
	data, err := os.ReadFile(filepath.Join(s.opts.Home, "auth.json"))
	if err != nil || string(data) != auth {
		t.Fatal("source credentials changed")
	}
}

func TestOAuthLoginCanRecoverInvalidSavedProvider(t *testing.T) {
	s, _ := testService(t)
	fixture(t, s.opts.Home, providerFile, `{broken-json`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	login, err := s.Login(ctx, analysis.LoginRequest{Type: "chatgpt"})
	if err != nil || login.AuthURL == "" {
		t.Fatalf("could not recover invalid provider: %v", err)
	}
}
