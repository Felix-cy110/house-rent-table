package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

type fakeAuth struct {
	input analysis.LoginRequest
	key   string
	kind  string
	fail  error
}

func (a *fakeAuth) Account(context.Context) (analysis.Account, error) {
	return analysis.Account{LoggedIn: a.key != ""}, a.fail
}
func (a *fakeAuth) Login(_ context.Context, input analysis.LoginRequest) (analysis.Login, error) {
	a.input = input
	a.kind = input.Type
	a.key = input.APIKey
	return analysis.Login{Type: input.Type}, a.fail
}

func TestProviderRequestsAndValidation(t *testing.T) {
	auth := &fakeAuth{}
	handler := New(Options{Auth: auth})
	for _, body := range []string{
		`{"type":"apiKey","apiKey":"test-only-secret","baseUrl":"https://gateway.example/v1","model":"chosen-model"}`,
		`{"type":"apiKey","source":"ccswitch"}`,
		`{"type":"apiKey","source":"ccswitch","configDir":"C:/Users/test/.codex"}`,
	} {
		r := httptest.NewRequest("POST", "/api/codex/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var want analysis.LoginRequest
		_ = json.Unmarshal([]byte(body), &want)
		if w.Code != 200 || auth.input != want || strings.Contains(w.Body.String(), "test-only-secret") {
			t.Fatal("provider request not forwarded safely")
		}
	}
	for _, body := range []string{
		`{"type":"apiKey","apiKey":"key","baseUrl":"file:///private","model":"m"}`,
		`{"type":"apiKey","apiKey":"key","baseUrl":"https://user:password@example.com","model":"m"}`,
		`{"type":"apiKey","apiKey":"key","baseUrl":"https://example.com?key=secret","model":"m"}`,
		`{"type":"apiKey","apiKey":"key","baseUrl":"https://example.com"}`,
		`{"type":"chatgpt","baseUrl":"https://example.com","model":"m"}`,
		`{"type":"apiKey","source":"ccswitch","apiKey":"mixed-secret"}`,
		`{"type":"apiKey","source":"unknown","apiKey":"key"}`,
	} {
		r := httptest.NewRequest("POST", "/api/codex/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Body.String(), "mixed-secret") {
			t.Fatal("invalid connection accepted or echoed")
		}
	}
}
func (a *fakeAuth) Logout(context.Context) error { a.key = ""; return a.fail }

func TestAuthEndpointsDoNotEchoSecrets(t *testing.T) {
	auth := &fakeAuth{}
	handler := New(Options{Auth: auth})
	r := httptest.NewRequest("POST", "/api/codex/login", strings.NewReader(`{"type":"apiKey","apiKey":"test-only-secret"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || auth.key != "test-only-secret" || strings.Contains(w.Body.String(), auth.key) {
		t.Fatalf("bad login response: %s", w.Body.String())
	}
	for _, path := range []string{"/api/codex/account", "/api/codex/logout"} {
		method := "GET"
		if strings.HasSuffix(path, "logout") {
			method = "POST"
		}
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "test-only-secret") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unsafe account response")
		}
	}
	if auth.key != "" {
		t.Fatal("logout not called")
	}
}

func TestInvalidLoginAndCrossOriginRequests(t *testing.T) {
	auth := &fakeAuth{}
	handler := New(Options{Auth: auth})
	for _, body := range []string{`{}`, `{"type":"apiKey","apiKey":" "}`, `{"type":"chatgpt","apiKey":"secret"}`, `{"type":"apiKey","apiKey":"a","extra":true}`, `{"type":"apiKey","apiKey":"a"}{}`} {
		r := httptest.NewRequest("POST", "/api/codex/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("bad input accepted: %d", w.Code)
		}
	}
	for _, path := range []string{"/api/codex/login", "/api/codex/logout", "/api/analyze"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://unrelated.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("cross-origin request accepted: %s %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	New(Options{Auth: auth, LocalOnly: true}).ServeHTTP(w, httptest.NewRequest("GET", "http://unrelated.example/api/codex/account", nil))
	if w.Code != http.StatusForbidden {
		t.Fatal("non-local host accepted")
	}
}

type errorAnalyzer struct{ err error }

func (a errorAnalyzer) Analyze(context.Context, json.RawMessage) (analysis.Result, error) {
	return analysis.Result{}, a.err
}

func TestAnalysisErrorsHaveUsefulStatusWithoutUpstreamSecrets(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{analysis.ErrLoginRequired, 401, "codex_login_required"}, {analysis.ErrBusy, 409, "codex_busy"},
		{analysis.ErrUnavailable, 503, "codex_unavailable"}, {context.DeadlineExceeded, 504, "codex_timeout"},
		{errors.New("upstream leaked-key-example"), 502, "codex_failed"},
	} {
		r := httptest.NewRequest("POST", "/api/analyze", strings.NewReader(`{"schemaVersion":1,"fileName":"test.xlsx","fields":[{"id":"f1","label":"任意字段","value":null,"sheet":"S","cell":"B2"}]}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		New(Options{Analyzer: errorAnalyzer{tt.err}}).ServeHTTP(w, r)
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.code) || strings.Contains(w.Body.String(), "leaked-key-example") {
			t.Fatalf("unexpected error: %d %s", w.Code, w.Body.String())
		}
	}
}
