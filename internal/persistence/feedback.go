package persistence

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidBrokerName = errors.New("请填写中介姓名，且姓名不能超过 256 个字符")

type BrokerFeedback struct {
	BrokerName    string `json:"brokerName"`
	FeedbackCount uint64 `json:"feedbackCount"`
}

func NormalizeBrokerName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 256 {
		return "", ErrInvalidBrokerName
	}
	return name, nil
}

// RecordBrokerFeedback increments under the unique name's row lock. Reading in
// the same transaction returns the count for this click, even with concurrent requests.
func (s *MySQL) RecordBrokerFeedback(ctx context.Context, name string) (BrokerFeedback, error) {
	name, err := NormalizeBrokerName(name)
	if err != nil {
		return BrokerFeedback{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BrokerFeedback{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO rental_broker_feedback (broker_name,feedback_count,created_at,updated_at)
 VALUES (?,1,?,?)
 ON DUPLICATE KEY UPDATE feedback_count=feedback_count+1,updated_at=?`, name, now, now, now)
	if err != nil {
		return BrokerFeedback{}, err
	}
	feedback := BrokerFeedback{BrokerName: name}
	if err = tx.QueryRowContext(ctx, "SELECT feedback_count FROM rental_broker_feedback WHERE broker_name=?", name).Scan(&feedback.FeedbackCount); err != nil {
		return BrokerFeedback{}, err
	}
	if err = tx.Commit(); err != nil {
		return BrokerFeedback{}, err
	}
	return feedback, nil
}
