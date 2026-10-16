package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/persistence"
)

type BrokerFeedbackStore interface {
	RecordBrokerFeedback(context.Context, string) (persistence.BrokerFeedback, error)
}

func registerBrokerFeedback(mux *http.ServeMux, store BrokerFeedbackStore) {
	mux.HandleFunc("POST /api/broker-feedback", func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请使用 JSON 提交坑人反馈")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var input struct {
			BrokerName string `json:"brokerName"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid_feedback", "反馈格式不正确，请提交中介姓名")
			return
		}
		name, err := persistence.NormalizeBrokerName(input.BrokerName)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_broker_name", err.Error())
			return
		}
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "feedback_disabled", "未启用反馈保存，请先配置 MySQL 并执行数据库迁移")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		feedback, err := store.RecordBrokerFeedback(ctx, name)
		if errors.Is(err, persistence.ErrInvalidBrokerName) {
			writeError(w, http.StatusBadRequest, "invalid_broker_name", persistence.ErrInvalidBrokerName.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "feedback_unavailable", "反馈未能确认保存，请检查数据库连接和迁移状态")
			return
		}
		writeJSON(w, http.StatusOK, feedback)
	})
}
