package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

func registerAuth(mux *http.ServeMux, auth analysis.Authenticator) {
	wrap := func(action func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if auth == nil {
				writeAgentError(w, analysis.ErrUnavailable)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			action(w, r.WithContext(ctx))
		}
	}
	mux.HandleFunc("GET /api/codex/account", wrap(func(w http.ResponseWriter, r *http.Request) {
		account, err := auth.Account(r.Context())
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, account)
	}))
	mux.HandleFunc("POST /api/codex/login", wrap(func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, 415, "invalid_content_type", "请使用 JSON 提交登录请求")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var input struct {
			Type   string `json:"type"`
			APIKey string `json:"apiKey"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || (input.Type != "chatgpt" && input.Type != "apiKey") || (input.Type == "apiKey" && strings.TrimSpace(input.APIKey) == "") || (input.Type == "chatgpt" && input.APIKey != "") {
			writeError(w, http.StatusBadRequest, "invalid_login", "请选择 OAuth 或填写有效的 API Key")
			return
		}
		result, err := auth.Login(r.Context(), input.Type, strings.TrimSpace(input.APIKey))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))
	mux.HandleFunc("POST /api/codex/logout", wrap(func(w http.ResponseWriter, r *http.Request) {
		if err := auth.Logout(r.Context()); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
}

func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, analysis.ErrLoginRequired):
		writeError(w, 401, "codex_login_required", "请先登录 Codex，再发送并分析")
	case errors.Is(err, analysis.ErrUnavailable):
		writeError(w, 503, "codex_unavailable", "无法连接 Codex，请确认已安装 Codex CLI，或检查服务的 Codex 路径配置")
	case errors.Is(err, analysis.ErrBusy):
		writeError(w, 409, "codex_busy", "Codex 正在处理请求，请完成或取消后重试")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 504, "codex_timeout", "等待 Codex 超时，本次请求已停止，请稍后重试")
	case errors.Is(err, context.Canceled):
		writeError(w, 408, "analysis_canceled", "本次分析已取消")
	default:
		writeError(w, 502, "codex_failed", "Codex 未完成请求，请检查登录状态、账号额度或网络后重试")
	}
}
