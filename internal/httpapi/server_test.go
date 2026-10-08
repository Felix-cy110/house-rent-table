package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/document"
	"github.com/Felix-cy110/house-rent-table/internal/importer"
	"github.com/Felix-cy110/house-rent-table/internal/testxlsx"
)

func uploadRequest(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestImportThenUnconfiguredAnalysis(t *testing.T) {
	handler := New(Options{})
	request := uploadRequest(t, "示例.xlsx", testxlsx.Bytes(t, [][]any{{"项目", "数值"}, {"未来新增字段", "0"}, {"空白", nil}}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private data must not be cached")
	}
	var imported importer.Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if len(imported.Document.Fields) != 2 || imported.Document.Fields[1].Value != nil {
		t.Fatal("dynamic document did not survive API")
	}
	payload, err := json.Marshal(imported.Document)
	if err != nil {
		t.Fatal(err)
	}
	analyze := httptest.NewRequest(http.MethodPost, "/api/analyze", bytes.NewReader(payload))
	analyze.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, analyze)
	if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), "analysis_not_configured") {
		t.Fatalf("want explicit not-configured state, got %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "findings") {
		t.Fatal("unconfigured agent must not return fabricated findings")
	}
}

func TestBadUploads(t *testing.T) {
	handler := New(Options{})
	for _, tt := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"test.xls", []byte("old format"), http.StatusUnsupportedMediaType},
		{"corrupt.xlsx", []byte("not a workbook"), http.StatusUnprocessableEntity},
		{"huge.xlsx", make([]byte, importer.MaxFileBytes+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, uploadRequest(t, tt.name, tt.data))
			if recorder.Code != tt.status {
				t.Fatalf("want %d got %d: %s", tt.status, recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader("broken multipart")))
	if recorder.Code != http.StatusBadRequest {
		t.Fatal("missing upload accepted")
	}
}

func TestAnalysisValidatesEnvelope(t *testing.T) {
	handler := New(Options{})
	valid := `{"schemaVersion":1,"fileName":"房源.xlsx","fields":[{"id":"f1","label":"月租","value":"0","sheet":"Sheet1","cell":"B2"}]}`
	for _, payload := range []string{
		`{"schemaVersion":2,"fields":[]}`,
		strings.Replace(valid, `"value":"0"`, `"value":0`, 1),
		strings.Replace(valid, `"schemaVersion":1`, `"extra":true,"schemaVersion":1`, 1),
		valid + ` {"extra":"second object"}`,
		`{"schemaVersion":1,"fields":[{"id":"f1","label":"A","value":null,"sheet":"S","cell":"B1"},{"id":"f1","label":"B","value":null,"sheet":"S","cell":"B2"}]}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/analyze", strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid document accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

type testAnalyzer struct{ received *document.Document }

func (a testAnalyzer) Analyze(_ context.Context, d document.Document) (analysis.Result, error) {
	*a.received = d
	return analysis.Result{Summary: "仅用于接口测试"}, nil
}

func TestAnalyzerReceivesWholeDocument(t *testing.T) {
	var received document.Document
	handler := New(Options{Analyzer: testAnalyzer{received: &received}})
	payload := `{"schemaVersion":1,"fileName":"房源.xlsx","fields":[{"id":"f1","label":"尚未定义的新项目","value":"原文","sheet":"Sheet1","cell":"B8"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/analyze", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK || len(received.Fields) != 1 || received.Fields[0].Label != "尚未定义的新项目" || received.Fields[0].Cell != "B8" {
		t.Fatal("analyzer did not receive the dynamic fields")
	}
	if !strings.Contains(w.Body.String(), `"findings":[]`) || !strings.Contains(w.Body.String(), `"missingInformation":[]`) {
		t.Fatal("empty result lists must serialize as arrays")
	}
}

func TestTemplateDownload(t *testing.T) {
	handler := New(Options{TemplatePath: "../../outputs/01a11ae7-a608-7692-9440-5edb4b06cfa9/租房信息模板.xlsx"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/template", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("template download failed: %d", w.Code)
	}
	if _, err := io.Copy(io.Discard, w.Result().Body); err != nil {
		t.Fatal(err)
	}
}
