package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

const probeInstructions = "This is a connection check. Reply only with OK. Do not call tools."

// Use a transient provider to test an OpenAI key before letting Codex persist it.
// No provider snapshot or existing OAuth/API key credential is changed here.
func (s *Service) probeOpenAIKey(ctx context.Context, key string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	home, err := s.prepareHome()
	if err != nil {
		return err
	}
	c, err := s.openClient(ctx, home, &providerConfig{BaseURL: "https://api.openai.com/v1", APIKey: key, Model: s.opts.Model})
	if err != nil {
		return err
	}
	defer c.close()
	return s.probeConnection(ctx, c)
}

// Caller holds the service gate and owns the candidate client. Acceptance of
// turn/start or a partial response alone does not establish a working connection.
func (s *Service) probeConnection(ctx context.Context, c *client) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	params := map[string]any{
		"cwd": s.workDir, "ephemeral": true, "sandbox": "read-only", "approvalPolicy": "never",
		"baseInstructions": probeInstructions, "config": map[string]any{"web_search": "disabled"},
	}
	if c.provider != nil && c.provider.Model != "" {
		params["model"] = c.provider.Model
	} else if s.opts.Model != "" {
		params["model"] = s.opts.Model
	}
	var thread struct {
		Thread struct{ ID string } `json:"thread"`
	}
	if err := c.call(ctx, "thread/start", params, &thread); err != nil {
		return probeCallError(err)
	}
	if thread.Thread.ID == "" {
		return probeCallError(analysis.ErrFailed)
	}
	events := make(chan message, 64)
	s.mu.Lock()
	s.threadID, s.events = thread.Thread.ID, events
	s.mu.Unlock()
	var started struct {
		Turn struct{ ID string } `json:"turn"`
	}
	terminal := false
	defer func() {
		s.mu.Lock()
		s.threadID, s.events = "", nil
		s.mu.Unlock()
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if !terminal && started.Turn.ID != "" {
			_ = c.call(cleanup, "turn/interrupt", map[string]string{"threadId": thread.Thread.ID, "turnId": started.Turn.ID}, nil)
		}
		_ = c.call(cleanup, "thread/unsubscribe", map[string]string{"threadId": thread.Thread.ID}, nil)
	}()
	if err := c.call(ctx, "turn/start", map[string]any{
		"threadId": thread.Thread.ID,
		"input":    []any{map[string]any{"type": "text", "text": "Reply with OK.", "text_elements": []any{}}},
	}, &started); err != nil {
		return probeCallError(err)
	}
	if started.Turn.ID == "" {
		return probeCallError(analysis.ErrFailed)
	}
	hasReply := false
	var lastError json.RawMessage
	for {
		select {
		case <-ctx.Done():
			return probeCallError(ctx.Err())
		case <-c.done:
			return probeCallError(analysis.ErrUnavailable)
		case event := <-events:
			var result struct {
				TurnID string                `json:"turnId"`
				Item   struct{ Text string } `json:"item"`
				Error  struct {
					Info json.RawMessage `json:"codexErrorInfo"`
				} `json:"error"`
				Turn struct {
					ID, Status string
					Error      struct {
						Info json.RawMessage `json:"codexErrorInfo"`
					} `json:"error"`
				} `json:"turn"`
			}
			if json.Unmarshal(event.Params, &result) != nil {
				continue
			}
			switch event.Method {
			case "item/completed":
				if result.TurnID == started.Turn.ID && strings.TrimSpace(result.Item.Text) != "" {
					hasReply = true
				}
			case "error":
				if result.TurnID == started.Turn.ID {
					// Retried failures are hints; a later completed turn still succeeds.
					lastError = result.Error.Info
				}
			case "turn/completed":
				if result.Turn.ID != started.Turn.ID {
					continue
				}
				terminal = true
				if result.Turn.Status == "completed" && hasReply {
					return nil
				}
				info := result.Turn.Error.Info
				if len(info) == 0 || string(info) == "null" || string(info) == `"other"` {
					info = lastError
				}
				return probeUpstreamError(info)
			}
		}
	}
}

func probeCallError(err error) error {
	message := "连接验证失败，模型未返回有效回复；请检查 API 地址、模型、Key 和 Responses API 支持情况后重试"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		message = "连接验证超时，请检查 API 地址、网络和供应商服务后重试"
	case errors.Is(err, context.Canceled):
		message = "连接验证已取消，请重试"
	case errors.Is(err, analysis.ErrUnavailable):
		message = "连接验证中断，请检查 Codex CLI 和供应商服务后重试"
	}
	return &analysis.ConnectionError{Message: message, Cause: err}
}

func probeUpstreamError(info json.RawMessage) error {
	var kind string
	var details map[string]struct {
		Status int `json:"httpStatusCode"`
	}
	var status int
	if json.Unmarshal(info, &kind) != nil && json.Unmarshal(info, &details) == nil {
		for _, name := range []string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts"} {
			if detail, ok := details[name]; ok {
				status = detail.Status
				break
			}
		}
	}
	var message string
	switch {
	case status == 401 || kind == "unauthorized":
		message = "连接验证失败：API Key 无效或已过期，请检查后重新填写"
	case status == 403:
		message = "连接验证失败：当前 Key 没有访问权限，请检查模型权限或供应商限制"
	case status == 404:
		message = "连接验证失败：API 地址或模型不存在，请检查基础地址和模型名称"
	case status == 400 || status == 422 || kind == "badRequest":
		message = "连接验证失败：供应商不接受请求，请检查 API 地址、模型名称和 Responses API 支持情况"
	case status == 429 || kind == "usageLimitExceeded" || kind == "rateLimitExceeded":
		message = "连接验证失败：额度不足或请求受限，请检查余额、配额后重试"
	case status >= 500 || kind == "serverOverloaded" || kind == "internalServerError":
		message = "连接验证失败：供应商服务暂时不可用，请稍后重试"
	default:
		return probeCallError(analysis.ErrFailed)
	}
	return &analysis.ConnectionError{Message: message}
}
