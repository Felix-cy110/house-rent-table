// Package persistence implements the local durable queue and MySQL snapshots.
package persistence

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/extraction"
	bolt "go.etcd.io/bbolt"
)

var ErrQueueFull = errors.New("后台保存队列已满")
var ErrNotFound = errors.New("保存任务不存在")
var taskBucket = []byte("tasks-v1")

type Task struct {
	ID              string           `json:"id"`
	Input           extraction.Input `json:"input"`
	RulesVersion    string           `json:"rules_version"`
	Stage           string           `json:"stage"`
	Status          string           `json:"status"`
	Attempts        uint32           `json:"attempts"`
	ExtractAttempts uint32           `json:"extract_attempts"`
	Result          json.RawMessage  `json:"result,omitempty"`
	Provider        string           `json:"provider"`
	Model           string           `json:"model"`
	ErrorCode       string           `json:"error_code"`
	ErrorMessage    string           `json:"error_message"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	CompletedAt     *time.Time       `json:"completed_at"`
	NextRun         time.Time        `json:"next_run"`
}

// Summary intentionally excludes source data, provider credentials and output.
type Summary struct {
	ID        string `json:"taskId"`
	Stage     string `json:"stage"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (t Task) Summary() Summary { return Summary{t.ID, t.Stage, t.Status, t.ErrorCode, t.ErrorMessage} }

type Queue struct {
	db       *bolt.DB
	wake     chan struct{}
	maxTasks int
	maxBytes int64
}

func OpenQueue(path string) (*Queue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second, MaxSize: 1 << 30})
	if err != nil {
		return nil, errors.New("无法打开后台任务队列（可能已有进程占用或目录不可写）")
	}
	q := &Queue{db: db, wake: make(chan struct{}, 1), maxTasks: 1000, maxBytes: 512 << 20}
	err = db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists(taskBucket)
		if e != nil {
			return e
		}
		recovered := map[string][]byte{}
		if e = b.ForEach(func(k, v []byte) error {
			var task Task
			if json.Unmarshal(v, &task) != nil || task.ID != string(k) || task.ID == "" {
				return errors.New("后台任务队列数据损坏")
			}
			if task.Status == "RUNNING" {
				task.Status = "PENDING"
				task.NextRun = time.Now().UTC()
				raw, e := json.Marshal(task)
				if e != nil {
					return e
				}
				recovered[string(k)] = raw
			}
			return nil
		}); e != nil {
			return e
		}
		// Cursor iteration must finish before changing any bucket entries.
		for key, raw := range recovered {
			if e = b.Put([]byte(key), raw); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return q, nil
}
func (q *Queue) Close() error { return q.db.Close() }
func (q *Queue) Enqueue(ctx context.Context, input extraction.Input) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := input.Document.Validate(); err != nil {
		return "", err
	}
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return "", err
	}
	uuid[6] = (uuid[6] & 15) | 64
	uuid[8] = (uuid[8] & 63) | 128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
	now := time.Now().UTC()
	t := Task{ID: id, Input: input, RulesVersion: extraction.RulesVersion, Stage: "EXTRACT", Status: "PENDING", CreatedAt: now, UpdatedAt: now, NextRun: now}
	raw, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	err = q.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(taskBucket)
		var count int
		size := int64(len(raw) + extraction.MaxResultBytes)
		if err := b.ForEach(func(_, v []byte) error { count++; size += int64(len(v) + extraction.MaxResultBytes); return nil }); err != nil {
			return err
		}
		if count >= q.maxTasks || size > q.maxBytes {
			return ErrQueueFull
		}
		return b.Put([]byte(id), raw)
	})
	if err != nil {
		return "", err
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return id, nil
}
func (q *Queue) Get(id string) (Task, error) {
	var t Task
	err := q.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(taskBucket).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		return json.Unmarshal(v, &t)
	})
	return t, err
}
func (q *Queue) save(t Task) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return q.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(taskBucket).Put([]byte(t.ID), raw) })
}
func (q *Queue) remove(id string) error {
	return q.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(taskBucket).Delete([]byte(id)) })
}
func (q *Queue) next(now time.Time) (*Task, error) {
	var chosen *Task
	err := q.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(taskBucket).ForEach(func(_, v []byte) error {
			var t Task
			if err := json.Unmarshal(v, &t); err != nil {
				return err
			}
			if !t.NextRun.After(now) && (chosen == nil || t.NextRun.Before(chosen.NextRun)) {
				chosen = &t
			}
			return nil
		})
	})
	return chosen, err
}
