package persistence

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/extraction"
)

func TestMigrationMatchesPublishedDesign(t *testing.T) {
	raw, err := os.ReadFile("../../docs/design/rental-persistence-v1/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), migrationSource()) {
		t.Fatal("published DDL differs from executable migration")
	}
}

// RENT_TEST_MYSQL_DSN must reference a dedicated disposable test database.
func TestMySQLTransactionsIdempotencyAndExactDecimals(t *testing.T) {
	dsn := os.Getenv("RENT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set RENT_TEST_MYSQL_DSN to a disposable MySQL database")
	}
	store, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("migration replay", err)
	}
	if err = store.CheckMigration(ctx); err != nil {
		t.Fatal(err)
	}
	input, raw := example(t)
	input.Document.FileName = strings.Repeat("文件", 40000) + ".xlsx"
	result, err := extraction.Decode(raw, input)
	if err != nil {
		t.Fatal(err)
	}
	q, err := OpenQueue(t.TempDir() + "/tasks.db")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	id, err := q.Enqueue(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := q.Get(id)
	task.Provider = strings.Repeat("供应方", 10000)
	task.Model = strings.Repeat("m", 256)
	task.Result = raw
	task.Stage = "PERSIST"
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- store.Complete(ctx, task, result) }()
	}
	wg.Wait()
	close(errs)
	for err = range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var fileName, provider, model string
	if err = store.db.QueryRowContext(ctx, "SELECT file_name,provider,model FROM rental_extraction_task WHERE id=?", id).Scan(&fileName, &provider, &model); err != nil {
		t.Fatal(err)
	}
	if fileName != input.Document.FileName || provider != task.Provider || model != task.Model {
		t.Fatal("accepted metadata truncated")
	}
	var records, charges int
	if err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rental_import_record WHERE task_id=?", id).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rental_charge c JOIN rental_import_record r ON r.id=c.record_id WHERE r.task_id=?", id).Scan(&charges); err != nil {
		t.Fatal(err)
	}
	if records != 1 || charges != 6 {
		t.Fatalf("duplicate rows: %d/%d", records, charges)
	}
	var rate string
	var included, free any
	if err = store.db.QueryRowContext(ctx, "SELECT CAST(c.unit_price AS CHAR) FROM rental_charge c JOIN rental_import_record r ON r.id=c.record_id WHERE r.task_id=? AND c.charge_type='ELECTRICITY'", id).Scan(&rate); err != nil || rate != "0.623000" {
		t.Fatalf("decimal changed: %s %v", rate, err)
	}
	_ = store.db.QueryRowContext(ctx, "SELECT c.amount FROM rental_charge c JOIN rental_import_record r ON r.id=c.record_id WHERE r.task_id=? AND c.charge_type='PROPERTY'", id).Scan(&included)
	_ = store.db.QueryRowContext(ctx, "SELECT CAST(c.amount AS CHAR) FROM rental_charge c JOIN rental_import_record r ON r.id=c.record_id WHERE r.task_id=? AND c.charge_type='CLEANING'", id).Scan(&free)
	if included != nil || string(free.([]byte)) != "0.00" {
		t.Fatal("included/free distinction lost")
	}
	task.Status = "RETRY_WAIT"
	task.NextRun = time.Now()
	if err = store.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	status, err := store.GetSummary(ctx, id)
	if err != nil || status.Status != "SUCCEEDED" {
		t.Fatal("late retry regressed committed status")
	}
	// A new user action on identical input must produce a separate snapshot.
	secondID, _ := q.Enqueue(ctx, input)
	second, _ := q.Get(secondID)
	second.Result = raw
	if err = store.Complete(ctx, second, result); err != nil {
		t.Fatal(err)
	}
	// Force failure after the parent INSERT; neither business table may retain partial data.
	triggerName := "test_charge_" + strings.ReplaceAll(id, "-", "")
	_, err = store.db.ExecContext(ctx, "CREATE TRIGGER "+triggerName+" BEFORE INSERT ON rental_charge FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='fixture failure'")
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.ExecContext(context.Background(), "DROP TRIGGER IF EXISTS "+triggerName)
	failedID, _ := q.Enqueue(ctx, input)
	failed, _ := q.Get(failedID)
	failed.Result = raw
	if err = store.Complete(ctx, failed, result); err == nil {
		t.Fatal("trigger failure ignored")
	}
	if err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rental_import_record WHERE task_id=?", failedID).Scan(&records); err != nil || records != 0 {
		t.Fatal("partial transaction committed")
	}
	status, err = store.GetSummary(ctx, failedID)
	if err != nil || status.Status == "SUCCEEDED" {
		t.Fatal("failed transaction marked complete")
	}
	if _, err = store.db.ExecContext(ctx, "DROP TRIGGER "+triggerName); err != nil {
		t.Fatal(err)
	}
	if err = store.Complete(ctx, failed, result); err != nil {
		t.Fatal("transaction recovery", err)
	}
}
