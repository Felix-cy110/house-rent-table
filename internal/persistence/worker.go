package persistence

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/extraction"
)

type Extractor interface {
	Extract(context.Context, extraction.Input) (extraction.Output, error)
}
type Store interface {
	SaveTask(context.Context, Task) error
	Complete(context.Context, Task, *extraction.ExtractionResult) error
	GetSummary(context.Context, string) (Summary, error)
}
type Worker struct {
	Queue     *Queue
	Extractor Extractor
	Store     Store
	Logf      func(string, ...any)
}

func (w *Worker) log(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for ctx.Err() == nil {
		t, err := w.Queue.next(time.Now().UTC())
		if err != nil {
			w.log("后台队列读取失败")
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			continue
		}
		if t != nil {
			w.process(ctx, t)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.Queue.wake:
		case <-timer.C:
		}
	}
}

// checkpoint retries disk writes without repeating the already finished AI call.
func (w *Worker) checkpoint(ctx context.Context, t Task) bool {
	for {
		if w.Queue.save(t) == nil {
			return true
		}
		w.log("后台任务 %s 落盘失败，保留本次结果并等待重试", t.ID)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}
func backoff(attempt uint32) time.Duration {
	if attempt > 8 {
		attempt = 8
	}
	return time.Second * time.Duration(1<<attempt)
}
func (w *Worker) retry(ctx context.Context, t *Task, code, msg string) {
	t.ErrorCode, t.ErrorMessage = code, msg
	t.UpdatedAt = time.Now().UTC()
	t.NextRun = t.UpdatedAt.Add(backoff(t.Attempts))
	if t.Status != "FAILED" {
		t.Status = "RETRY_WAIT"
	}
	w.log("后台任务 %s：%s", t.ID, msg)
	w.checkpoint(ctx, *t)
}
func (w *Worker) process(ctx context.Context, t *Task) {
	if ctx.Err() != nil {
		return
	}
	if t.Status == "FAILED" {
		dbctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := w.Store.SaveTask(dbctx, *t)
		cancel()
		if err != nil {
			t.Attempts++
			w.retry(ctx, t, t.ErrorCode, t.ErrorMessage)
			return
		}
		if err = w.Queue.remove(t.ID); err != nil {
			w.log("后台任务 %s 已留档，本地清理待重试", t.ID)
			t.Attempts++
			w.retry(ctx, t, t.ErrorCode, t.ErrorMessage)
		}
		return
	}
	t.Status = "RUNNING"
	t.Attempts++
	t.UpdatedAt = time.Now().UTC()
	if !w.checkpoint(ctx, *t) {
		return
	}
	// MySQL availability must not determine whether the extraction can proceed.
	dbctx, dbCancel := context.WithTimeout(ctx, 3*time.Second)
	_ = w.Store.SaveTask(dbctx, *t)
	dbCancel()
	if len(t.Result) == 0 {
		if t.RulesVersion != extraction.RulesVersion {
			w.fail(ctx, t, "rules_version_changed", "提取规则已变更，请重新发起分析")
			return
		}
		t.ExtractAttempts++
		if !w.checkpoint(ctx, *t) {
			return
		}
		aiCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		out, err := w.Extractor.Extract(aiCtx, t.Input)
		cancel()
		t.Provider, t.Model = out.Provider, out.Model
		if ctx.Err() != nil && err != nil {
			t.Status = "PENDING"
			t.NextRun = time.Now().UTC()
			_ = w.Queue.save(*t)
			return
		}
		code, msg := "extraction_failed", "AI 结构化提取暂时失败"
		if err == nil {
			_, err = extraction.Decode(out.JSON, t.Input)
			if err != nil {
				code, msg = "invalid_extraction", "AI 结构化结果未通过业务校验"
			}
		}
		if err != nil {
			if t.ExtractAttempts >= 3 {
				w.fail(ctx, t, code, msg+"，已停止自动提取")
			} else {
				w.retry(ctx, t, code, msg)
			}
			return
		}
		t.Result = append([]byte(nil), out.JSON...)
		t.Stage = "PERSIST"
		t.Status = "PENDING"
		t.ErrorCode = ""
		t.ErrorMessage = ""
		t.UpdatedAt = time.Now().UTC()
		if !w.checkpoint(ctx, *t) {
			return
		}
	}
	result, err := extraction.Decode(t.Result, t.Input)
	if err != nil {
		w.fail(ctx, t, "invalid_checkpoint", "已保存的结构化结果无法通过当前契约校验")
		return
	}
	dbctx, dbCancel = context.WithTimeout(ctx, 10*time.Second)
	err = w.Store.Complete(dbctx, *t, result)
	dbCancel()
	if err != nil {
		w.retry(ctx, t, "mysql_unavailable", "MySQL 写入失败，已保留提取结果等待补写")
		return
	}
	if err = w.Queue.remove(t.ID); err != nil {
		w.retry(ctx, t, "queue_cleanup_failed", "数据已入库，本地任务清理待重试")
	}
}
func (w *Worker) fail(ctx context.Context, t *Task, code, msg string) {
	now := time.Now().UTC()
	t.Status = "FAILED"
	t.ErrorCode = code
	t.ErrorMessage = msg
	t.CompletedAt = &now
	t.UpdatedAt = now
	t.NextRun = now
	w.log("后台任务 %s：%s", t.ID, msg)
	w.checkpoint(ctx, *t)
}
func (w *Worker) GetSummary(ctx context.Context, id string) (Summary, error) {
	t, err := w.Queue.Get(id)
	if err == nil {
		return t.Summary(), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Summary{}, err
	}
	return w.Store.GetSummary(ctx, id)
}
