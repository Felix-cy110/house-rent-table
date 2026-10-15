package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/extraction"
)

func example(t *testing.T) (extraction.Input, []byte) {
	t.Helper()
	a, e := os.ReadFile("../../docs/design/rental-persistence-v1/input.example.json")
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("../../docs/design/rental-persistence-v1/extraction.example.json")
	if e != nil {
		t.Fatal(e)
	}
	var input extraction.Input
	if e = json.Unmarshal(a, &input); e != nil {
		t.Fatal(e)
	}
	return input, b
}

type fakeExtractor struct {
	calls int
	raw   []byte
	err   error
	after func()
}

func (f *fakeExtractor) Extract(context.Context, extraction.Input) (extraction.Output, error) {
	f.calls++
	if f.after != nil {
		f.after()
	}
	return extraction.Output{JSON: f.raw, Provider: "fixture", Model: "fixture"}, f.err
}

type fakeStore struct {
	fail          bool
	completeCalls int
	saved         map[string]Summary
}

func (f *fakeStore) SaveTask(_ context.Context, t Task) error {
	if f.fail {
		return errors.New("offline")
	}
	if f.saved == nil {
		f.saved = map[string]Summary{}
	}
	f.saved[t.ID] = t.Summary()
	return nil
}
func (f *fakeStore) Complete(_ context.Context, t Task, _ *extraction.ExtractionResult) error {
	f.completeCalls++
	if f.fail {
		return errors.New("offline")
	}
	t.Status = "SUCCEEDED"
	return f.SaveTask(context.Background(), t)
}
func (f *fakeStore) GetSummary(_ context.Context, id string) (Summary, error) {
	s, ok := f.saved[id]
	if !ok {
		return Summary{}, ErrNotFound
	}
	return s, nil
}
func quiet(string, ...any) {}

func TestMySQLOutageRestartReplaysWithoutAnotherAIRequest(t *testing.T) {
	input, raw := example(t)
	path := filepath.Join(t.TempDir(), "queue.db")
	q, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := q.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	ai := &fakeExtractor{raw: raw}
	store := &fakeStore{fail: true}
	w := Worker{Queue: q, Extractor: ai, Store: store, Logf: quiet}
	task, _ := q.Get(id)
	w.process(context.Background(), &task)
	saved, err := q.Get(id)
	if err != nil || saved.Stage != "PERSIST" || len(saved.Result) == 0 || saved.Status != "RETRY_WAIT" || ai.calls != 1 {
		t.Fatalf("checkpoint lost: %+v %v", saved.Summary(), err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	w.Queue = q
	store.fail = false
	restored, _ := q.Get(id)
	w.process(context.Background(), &restored)
	if ai.calls != 1 || store.completeCalls != 2 {
		t.Fatal("database retry called AI again")
	}
	if _, err = q.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatal("committed checkpoint not removed")
	}
	summary, err := w.GetSummary(context.Background(), id)
	if err != nil || summary.Status != "SUCCEEDED" {
		t.Fatal("saved status unavailable")
	}
}
func TestFailureIsRetainedWithoutBusinessInsertion(t *testing.T) {
	input, _ := example(t)
	q, err := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	id, _ := q.Enqueue(context.Background(), input)
	ai := &fakeExtractor{raw: []byte(`{}`)}
	store := &fakeStore{}
	w := Worker{Queue: q, Extractor: ai, Store: store, Logf: quiet}
	for i := 0; i < 3; i++ {
		task, _ := q.Get(id)
		w.process(context.Background(), &task)
	}
	task, _ := q.Get(id)
	if task.Status != "FAILED" || task.CompletedAt == nil || ai.calls != 3 || store.completeCalls != 0 {
		t.Fatal("failed extraction created business data or unbounded retries")
	}
	w.process(context.Background(), &task)
	if store.saved[id].Status != "FAILED" {
		t.Fatal("failure was not logged")
	}
}
func TestShutdownKeepsSuccessfulExtractionCheckpoint(t *testing.T) {
	input, raw := example(t)
	q, err := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	id, err := q.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ai := &fakeExtractor{raw: raw, after: cancel}
	store := &fakeStore{fail: true}
	w := Worker{Queue: q, Extractor: ai, Store: store, Logf: quiet}
	task, _ := q.Get(id)
	w.process(ctx, &task)
	task, err = q.Get(id)
	if err != nil || len(task.Result) == 0 || task.Stage != "PERSIST" {
		t.Fatal("shutdown discarded a successful extraction")
	}
	store.fail = false
	w.process(context.Background(), &task)
	if ai.calls != 1 {
		t.Fatal("shutdown replay called AI again")
	}
}

func TestQueueAdmissionIsolationAndRecovery(t *testing.T) {
	input, _ := example(t)
	path := filepath.Join(t.TempDir(), "queue.db")
	q, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := q.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := q.Enqueue(context.Background(), input)
	if err != nil || id == id2 {
		t.Fatal("separate user actions deduplicated")
	}
	original := *input.Document.Fields[0].Value
	*input.Document.Fields[0].Value = "changed"
	task, _ := q.Get(id)
	if *task.Input.Document.Fields[0].Value != original {
		t.Fatal("queue retained mutable input")
	}
	q.maxTasks = 2
	if _, err = q.Enqueue(context.Background(), input); !errors.Is(err, ErrQueueFull) {
		t.Fatal("full queue accepted task")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = q.Enqueue(ctx, input); err == nil {
		t.Fatal("canceled admission accepted")
	}
	task.Status = "RUNNING"
	task.NextRun = time.Now().Add(time.Hour)
	if err = q.save(task); err != nil {
		t.Fatal(err)
	}
	second, _ := q.Get(id2)
	second.Status = "RUNNING"
	second.NextRun = time.Now().Add(time.Hour)
	if err = q.save(second); err != nil {
		t.Fatal(err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for _, recoveredID := range []string{id, id2} {
		task, err = q.Get(recoveredID)
		if err != nil || task.Status != "PENDING" || task.NextRun.After(time.Now()) {
			t.Fatal("interrupted running task was not recovered")
		}
	}
}
