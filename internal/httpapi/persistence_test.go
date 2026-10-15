package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/persistence"
)

type failingMain struct{}

func (failingMain) Analyze(context.Context, json.RawMessage) (analysis.Result, error) {
	return analysis.Result{}, analysis.ErrFailed
}
func TestAnalysisAdmissionDoesNotChangeMainResponse(t *testing.T) {
	q, err := persistence.OpenQueue(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	var received json.RawMessage
	payload := `{"schemaVersion":1,"fileName":"private.xlsx","fields":[{"id":"f1","label":"月租","value":"2500","bold":false,"sheet":"S","cell":"B1"}]}`
	worker := &persistence.Worker{Queue: q}
	call := func(handler http.Handler, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/analyze", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	handler := New(Options{Analyzer: testAnalyzer{received: &received}, Persistence: worker})
	rec := call(handler, payload)
	id := rec.Header().Get("X-Rental-Task-Id")
	if rec.Code != 200 || rec.Header().Get("X-Rental-Persistence") != "queued" || string(received) != payload {
		t.Fatal("main path changed")
	}
	task, err := q.Get(id)
	if err != nil || task.Input.Document.FileName != "private.xlsx" {
		t.Fatal("input not queued")
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest("GET", "/api/persistence/tasks/"+id, nil))
	if status.Code != 200 || strings.Contains(status.Body.String(), "private.xlsx") || strings.Contains(status.Body.String(), "2500") {
		t.Fatal("status leaked source data")
	}
	failed := call(New(Options{Analyzer: failingMain{}, Persistence: worker}), payload)
	if failed.Code == 200 || failed.Header().Get("X-Rental-Task-Id") == "" {
		t.Fatal("main failure prevented background admission")
	}
	bad := call(handler, `{}`)
	if bad.Code != 400 || bad.Header().Get("X-Rental-Task-Id") != "" {
		t.Fatal("invalid request queued")
	}
	q.Close()
	rec = call(handler, payload)
	if rec.Code != 200 || rec.Header().Get("X-Rental-Persistence") != "failed" {
		t.Fatal("queue failure broke main analysis")
	}
	if _, err = q.Get(id); errors.Is(err, persistence.ErrNotFound) {
		t.Fatal("test expected closed queue")
	}
}
