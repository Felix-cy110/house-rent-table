package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Felix-cy110/house-rent-table/internal/persistence"
)

type feedbackStore struct {
	names map[string]uint64
	err   error
}

func (s *feedbackStore) RecordBrokerFeedback(ctx context.Context, name string) (persistence.BrokerFeedback, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("feedback requests must have a timeout")
	}
	if s.err != nil {
		return persistence.BrokerFeedback{}, s.err
	}
	s.names[name]++
	return persistence.BrokerFeedback{BrokerName: name, FeedbackCount: s.names[name]}, nil
}

func feedbackRequest(handler http.Handler, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/broker-feedback", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestBrokerFeedbackCountsAndErrors(t *testing.T) {
	store := &feedbackStore{names: map[string]uint64{}}
	handler := New(Options{BrokerFeedback: store})
	for count := uint64(1); count <= 2; count++ {
		rec := feedbackRequest(handler, `{"brokerName":"  张三　"}`, "application/json; charset=utf-8")
		var feedback persistence.BrokerFeedback
		if err := json.Unmarshal(rec.Body.Bytes(), &feedback); err != nil || rec.Code != http.StatusOK || feedback.BrokerName != "张三" || feedback.FeedbackCount != count {
			t.Fatalf("feedback %d: %d %s (%v)", count, rec.Code, rec.Body, err)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("feedback response cached")
		}
	}
	store.err = errors.New("mysql password=private_secret connection failed")
	rec := feedbackRequest(handler, `{"brokerName":"张三"}`, "application/json")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "feedback_unavailable") || strings.Contains(rec.Body.String(), "private_secret") || store.names["张三"] != 2 {
		t.Fatalf("database failure: %d %s", rec.Code, rec.Body)
	}
	rec = feedbackRequest(New(Options{}), `{"brokerName":"张三"}`, "application/json")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "feedback_disabled") {
		t.Fatalf("disabled feedback: %d %s", rec.Code, rec.Body)
	}
}

func TestBrokerFeedbackRejectsInvalidRequests(t *testing.T) {
	store := &feedbackStore{names: map[string]uint64{}}
	handler := New(Options{BrokerFeedback: store})
	for _, body := range []string{
		`{}`, `null`, `[]`, `{`, `{"brokerName":null}`, `{"brokerName":42}`,
		`{"brokerName":" \t\n　"}`, `{"brokerName":"张三","extra":1}`,
		`{"brokerName":"张三"} {"brokerName":"李四"}`,
		`{"brokerName":"` + strings.Repeat("介", 257) + `"}`,
		`{"brokerName":"` + strings.Repeat("x", 9<<10) + `"}`,
	} {
		rec := feedbackRequest(handler, body, "application/json")
		if rec.Code != 400 {
			t.Errorf("invalid input accepted: status=%d body=%s", rec.Code, rec.Body)
		}
	}
	if len(store.names) != 0 {
		t.Fatal("invalid request reached storage")
	}
	rec := feedbackRequest(handler, `{"brokerName":"张三"}`, "text/plain")
	if rec.Code != 415 {
		t.Fatal("non-JSON accepted")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/broker-feedback", strings.NewReader(`{"brokerName":"张三"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 403 || len(store.names) != 0 {
		t.Fatal("cross-origin feedback accepted")
	}
}
