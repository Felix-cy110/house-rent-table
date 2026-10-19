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
	fail  error
}

func (a *fakeAuth) Account(context.Context) (analysis.Account, error) {
	return analysis.Account{Connected: a.key != ""}, a.fail
}
func (a *fakeAuth) Login(_ context.Context, input analysis.LoginRequest) (analysis.Login, error) {
	a.input = input
	a.key = input.APIKey
	return analysis.Login{OK: true}, a.fail
}

func TestProviderRequestsAndValidation(t *testing.T) {
	auth := &fakeAuth{}
	handler := New(Options{Auth: auth})
	for _, body := range []string{
		`{"apiKey":"test-only-secret","provider":"mimo"}`,
		`{"apiKey":"test-only-secret","provider":"deepseek"}`,
	} {
		r := httptest.NewRequest("POST", "/api/ai/connect", strings.NewReader(body))
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
		`{"apiKey":"key","provider":"mimo","baseUrl":"https://evil.example"}`,
		`{"apiKey":"key","provider":"glm","model":"arbitrary"}`,
		`{"apiKey":"key","provider":"unsupported"}`,
		`{"apiKey":"key","baseUrl":"file:///private","model":"m"}`,
		`{"apiKey":"key","baseUrl":"https://user:password@example.com","model":"m"}`,
		`{"apiKey":"key","baseUrl":"https://example.com?key=secret","model":"m"}`,
		`{"apiKey":"key","baseUrl":"https://example.com"}`,
		`{"type":"chatgpt","baseUrl":"https://example.com","model":"m"}`,
		`{"type":"apiKey","source":"ccswitch","apiKey":"mixed-secret"}`,
		`{"type":"apiKey","source":"unknown","apiKey":"key"}`,
	} {
		r := httptest.NewRequest("POST", "/api/ai/connect", strings.NewReader(body))
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
	r := httptest.NewRequest("POST", "/api/ai/connect", strings.NewReader(`{"apiKey":"test-only-secret","provider":"glm"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || auth.key != "test-only-secret" || strings.Contains(w.Body.String(), auth.key) {
		t.Fatalf("bad login response: %s", w.Body.String())
	}
	for _, path := range []string{"/api/ai/config", "/api/ai/disconnect"} {
		method := "GET"
		if strings.HasSuffix(path, "disconnect") {
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
		r := httptest.NewRequest("POST", "/api/ai/connect", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("bad input accepted: %d", w.Code)
		}
	}
	for _, path := range []string{"/api/ai/connect", "/api/ai/disconnect", "/api/analyze"} {
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
	New(Options{Auth: auth, LocalOnly: true}).ServeHTTP(w, httptest.NewRequest("GET", "http://unrelated.example/api/ai/config", nil))
	if w.Code != http.StatusForbidden {
		t.Fatal("non-local host accepted")
	}
}

func TestConnectionValidationErrorsReachBrowserWithoutUpstreamDetails(t *testing.T) {
	for _, tt := range []struct {
		cause  error
		status int
	}{
		{errors.New("upstream-secret-must-not-leak"), http.StatusBadGateway},
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
		{context.Canceled, http.StatusRequestTimeout},
	} {
		auth := &fakeAuth{fail: &analysis.ConnectionError{Message: "连接验证失败，请检查配置后重试", Cause: tt.cause}}
		r := httptest.NewRequest("POST", "/api/ai/connect", strings.NewReader(`{"apiKey":"test-only-secret","provider":"glm"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		New(Options{Auth: auth}).ServeHTTP(w, r)
		if w.Code != tt.status || !strings.Contains(w.Body.String(), "connection_failed") || !strings.Contains(w.Body.String(), "连接验证失败") || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unsafe or incorrect validation error: %d %s", w.Code, w.Body.String())
		}
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
		{analysis.ErrLoginRequired, 401, "ai_config_required"}, {analysis.ErrBusy, 409, "ai_busy"},
		{analysis.ErrUnavailable, 503, "ai_unavailable"}, {context.DeadlineExceeded, 504, "ai_timeout"},
		{analysis.ErrSearchUnavailable, 503, "search_unavailable"},
		{errors.New("upstream leaked-key-example"), 502, "ai_failed"},
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

func TestFixedModelCatalogHidesEndpointsAndCredentials(t *testing.T) {
	w := httptest.NewRecorder()
	New(Options{}).ServeHTTP(w, httptest.NewRequest("GET", "/api/ai/models", nil))
	var body struct {
		Providers []struct {
			ID, Model, VersionNote string
			Thinking               bool
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Providers) != 3 {
		t.Fatal("model catalog unavailable")
	}
	for _, p := range body.Providers {
		if !p.Thinking {
			t.Fatal("thinking disabled in catalog")
		}
	}
	if body.Providers[2].Model != "deepseek-flash" || !strings.Contains(body.Providers[2].VersionNote, "V4.1-Flash-1017") || strings.Contains(w.Body.String(), "https:") || strings.Contains(w.Body.String(), "apiKey") {
		t.Fatal("unsafe or stale catalog")
	}
}
