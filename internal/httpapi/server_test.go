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
	"github.com/Felix-cy110/house-rent-table/internal/importer"
	"github.com/Felix-cy110/house-rent-table/internal/testxlsx"
	"github.com/xuri/excelize/v2"
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
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "ai_unavailable") {
		t.Fatalf("want explicit not-configured state, got %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `"text"`) {
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
		strings.Replace(valid, `"label":"月租"`, `"label":"月租","bold":"true"`, 1),
		strings.Replace(valid, `"label":"月租"`, `"label":"月租","bold":1`, 1),
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

type testAnalyzer struct{ received *json.RawMessage }

func (a testAnalyzer) Analyze(_ context.Context, d json.RawMessage) (analysis.Result, error) {
	*a.received = d
	return analysis.Result{Text: "仅用于接口测试\n原始回复"}, nil
}

func TestAnalyzerReceivesWholeDocument(t *testing.T) {
	var received json.RawMessage
	handler := New(Options{Analyzer: testAnalyzer{received: &received}})
	payload := `{"schemaVersion":1,"fileName":"房源.xlsx","fields":[{"id":"f1","label":"尚未定义的新项目","bold":true,"value":"原文","sheet":"Sheet1","cell":"B8"},{"id":"f2","label":"依据项目","bold":false,"value":"0","sheet":"Sheet1","cell":"B9"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/analyze", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK || string(received) != payload {
		t.Fatal("analyzer did not receive the exact request bytes")
	}
	if !strings.Contains(w.Body.String(), `"text":"仅用于接口测试\n原始回复"`) {
		t.Fatal("original response text was not preserved")
	}
}

func TestTemplateDownload(t *testing.T) {
	var received json.RawMessage
	handler := New(Options{
		TemplatePath: "../../outputs/01a11ae7-a608-7692-9440-5edb4b06cfa9/租房信息模板.xlsx",
		Analyzer:     testAnalyzer{received: &received},
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/template", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("template download failed: %d", w.Code)
	}
	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	value := "3150元/月，含物业费\n水电按实际用量另付"
	if err := workbook.SetCellValue("Sheet1", "B12", value); err != nil {
		t.Fatal(err)
	}
	for cell, value := range map[string]any{"B2": "  12个月（固定租期）  ", "B14": 28.5, "B15": "南北通透", "B16": "测试中介"} {
		if err := workbook.SetCellValue("Sheet1", cell, value); err != nil {
			t.Fatal(err)
		}
	}
	filled, err := workbook.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, uploadRequest(t, "每月费用.xlsx", filled.Bytes()))
	if w.Code != http.StatusOK {
		t.Fatalf("downloaded template import failed: %d %s", w.Code, w.Body.String())
	}
	var imported importer.Result
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if len(imported.Document.Fields) != 15 {
		t.Fatalf("downloaded template field count: %d", len(imported.Document.Fields))
	}
	lease := imported.Document.Fields[0]
	if lease.Label != "租几个月" || lease.Cell != "B2" || lease.Sheet != "Sheet1" || lease.Bold || lease.Value == nil || *lease.Value != "  12个月（固定租期）  " {
		t.Fatalf("lease duration or source changed: %+v", lease)
	}
	monthly := imported.Document.Fields[10]
	if !imported.Document.Fields[9].Bold || monthly.Bold {
		t.Fatal("downloaded template's bold target was lost during import")
	}
	if imported.Document.Fields[9].Label != "首月总共要付多少" || monthly.Label != "以后每个月要付多少钱" ||
		monthly.Value == nil || *monthly.Value != value || monthly.Sheet != "Sheet1" || monthly.Cell != "B12" ||
		imported.Document.Fields[11].Label != "小区地点" || imported.Document.Fields[11].Cell != "B13" {
		t.Fatalf("monthly payment or adjacent fields changed: %+v", imported.Document.Fields)
	}
	for offset, want := range []struct{ label, value, cell string }{
		{"房间大小/平方", "28.5", "B14"},
		{"朝向", "南北通透", "B15"},
		{"中介姓名", "测试中介", "B16"},
	} {
		field := imported.Document.Fields[12+offset]
		if field.Label != want.label || field.Value == nil || *field.Value != want.value ||
			field.Cell != want.cell || field.Sheet != "Sheet1" || field.Bold {
			t.Fatalf("room detail or following agent changed: %+v", field)
		}
	}
	payload, err := json.Marshal(imported.Document)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/analyze", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK || !bytes.Equal(received, payload) {
		t.Fatalf("template fields were not forwarded intact: %d %s", w.Code, w.Body.String())
	}
}
