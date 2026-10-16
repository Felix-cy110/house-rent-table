package persistence

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeBrokerName(t *testing.T) {
	for _, name := range []string{"", " \t\n\u3000", strings.Repeat("中", 257), string([]byte{0xff})} {
		if _, err := NormalizeBrokerName(name); !errors.Is(err, ErrInvalidBrokerName) {
			t.Errorf("invalid name accepted: %q", name)
		}
	}
	for _, name := range []string{"张三", "O'Connor", strings.Repeat("介", 256)} {
		got, err := NormalizeBrokerName(" \u3000" + name + "\n")
		if err != nil || got != name {
			t.Errorf("name changed: %q, %v", got, err)
		}
	}
}

func TestMySQLBrokerFeedback(t *testing.T) {
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
	name := "测试中介 O'Connor🏠 " + time.Now().UTC().Format("20060102150405.000000000")
	defer store.db.ExecContext(context.Background(), "DELETE FROM rental_broker_feedback WHERE broker_name IN (?,?,?,?)", name, name+"甲", name+"a", name+"á")
	for count := uint64(1); count <= 2; count++ {
		feedback, err := store.RecordBrokerFeedback(ctx, " \u3000"+name+"\n")
		if err != nil || feedback.BrokerName != name || feedback.FeedbackCount != count {
			t.Fatalf("click %d: %+v %v", count, feedback, err)
		}
	}
	// Different names have independent counts, including names that an accent-
	// insensitive database collation would otherwise merge.
	for _, other := range []string{name + "甲", name + "a", name + "á"} {
		feedback, err := store.RecordBrokerFeedback(ctx, other)
		if err != nil || feedback.FeedbackCount != 1 {
			t.Fatalf("independent name: %+v %v", feedback, err)
		}
	}
	const clicks = 32
	var wg sync.WaitGroup
	results := make(chan BrokerFeedback, clicks)
	errs := make(chan error, clicks)
	for i := 0; i < clicks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			feedback, err := store.RecordBrokerFeedback(ctx, name)
			results <- feedback
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uint64]bool{}
	for feedback := range results {
		if feedback.BrokerName != name || feedback.FeedbackCount < 3 || feedback.FeedbackCount > clicks+2 || seen[feedback.FeedbackCount] {
			t.Fatalf("lost or duplicated increment: %+v", feedback)
		}
		seen[feedback.FeedbackCount] = true
	}
	var rows int
	var count uint64
	if err = store.db.QueryRowContext(ctx, "SELECT COUNT(*),MAX(feedback_count) FROM rental_broker_feedback WHERE broker_name=?", name).Scan(&rows, &count); err != nil || rows != 1 || count != clicks+2 {
		t.Fatalf("stored count: rows=%d count=%d err=%v", rows, count, err)
	}
	if _, err = store.RecordBrokerFeedback(ctx, " "); !errors.Is(err, ErrInvalidBrokerName) {
		t.Fatal("blank name accepted", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err = store.RecordBrokerFeedback(canceled, name); err == nil {
		t.Fatal("canceled feedback accepted")
	}
	if err = store.db.QueryRowContext(ctx, "SELECT feedback_count FROM rental_broker_feedback WHERE broker_name=?", name).Scan(&count); err != nil || count != clicks+2 {
		t.Fatal("canceled or invalid feedback changed count", count, err)
	}
}
