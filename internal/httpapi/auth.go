package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/modelconfig"
)

func registerAuth(mux *http.ServeMux, auth analysis.Authenticator) {
	mux.HandleFunc("GET /api/ai/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"providers": modelconfig.All()})
	})
	wrap := func(timeout time.Duration, action func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if auth == nil {
				writeAgentError(w, analysis.ErrUnavailable)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			action(w, r.WithContext(ctx))
		}
	}
	mux.HandleFunc("GET /api/ai/config", wrap(20*time.Second, func(w http.ResponseWriter, r *http.Request) {
		account, err := auth.Account(r.Context())
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, account)
	}))
	mux.HandleFunc("POST /api/ai/connect", wrap(120*time.Second, func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, 415, "invalid_content_type", "请使用 JSON 提交 API 配置")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var input analysis.LoginRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid_connection", "请选择受支持的模型并填写 API Key；不接受自定义 URL 或模型名")
			return
		}
		if err := input.Validate(); err != nil {
			writeAgentError(w, err)
			return
		}
		result, err := auth.Login(r.Context(), input)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))
	mux.HandleFunc("POST /api/ai/disconnect", wrap(20*time.Second, func(w http.ResponseWriter, r *http.Request) {
		if err := auth.Logout(r.Context()); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
}

func writeAgentError(w http.ResponseWriter, err error) {
	var connectionError *analysis.ConnectionError
	if errors.As(err, &connectionError) {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, context.Canceled) {
			status = http.StatusRequestTimeout
		}
		writeError(w, status, "connection_failed", connectionError.Message)
		return
	}
	var configError *analysis.ConfigError
	if errors.As(err, &configError) {
		writeError(w, http.StatusBadRequest, "invalid_provider", configError.Message)
		return
	}
	switch {
	case errors.Is(err, analysis.ErrLoginRequired):
		writeError(w, 401, "ai_config_required", "请先选择模型并配置 API Key，再发送并分析")
	case errors.Is(err, analysis.ErrUnavailable):
		writeError(w, 503, "ai_unavailable", "无法读取 API 配置，请重新填写并保存，或检查服务配置目录")
	case errors.Is(err, analysis.ErrSearchUnavailable):
		writeError(w, 503, "search_unavailable", "MiMo 搜索插件未开通，且服务端未配置备用搜索；请联系维护者配置备用搜索，或在小米平台开通插件后重试")
	case errors.Is(err, analysis.ErrBusy):
		writeError(w, 409, "ai_busy", "AI 正在处理请求，请完成或取消后重试")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 504, "ai_timeout", "等待 AI 回复超时，本次请求已停止，请稍后重试")
	case errors.Is(err, context.Canceled):
		writeError(w, 408, "analysis_canceled", "本次分析已取消")
	default:
		writeError(w, 502, "ai_failed", "AI 未完成请求，请检查 API 配置、额度或网络后重试")
	}
}
