package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/extraction"
	"github.com/Felix-cy110/house-rent-table/internal/review"
)

const maxResponseBytes = extraction.MaxResultBytes + (4 << 20)
const maxToolRounds = 16

// apiError contains only fixed local text, never a provider body, URL or key.
type apiError struct{ status int }

func (e *apiError) Error() string {
	switch e.status {
	case 401:
		return "API Key 无效或已过期，请检查后重新填写"
	case 403:
		return "当前 Key 没有访问权限，请检查模型权限或供应商限制"
	case 404:
		return "API URL 或模型不存在，请检查地址和模型名称"
	case 400, 405, 422:
		return "供应商不接受请求，请检查模型、接口协议及工具或结构化输出支持情况"
	case 429:
		return "额度不足或请求受限，请检查余额、配额后重试"
	default:
		return "供应商未完成请求，请检查 API URL、网络和供应商服务后重试"
	}
}
func (e *apiError) Unwrap() error { return analysis.ErrFailed }

func (s *Service) post(ctx context.Context, c analysis.LoginRequest, body any, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return analysis.ErrFailed
	}
	address, _ := endpoint(c.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(raw))
	if err != nil {
		return analysis.ErrFailed
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &apiError{}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{status: resp.StatusCode}
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return analysis.ErrFailed
	}
	if len(raw) > maxResponseBytes {
		return analysis.ErrFailed
	}
	if json.Unmarshal(raw, result) != nil {
		return analysis.ErrFailed
	}
	return nil
}

func (s *Service) complete(ctx context.Context, c analysis.LoginRequest, instructions, input string, schema json.RawMessage, tools bool) (analysis.Result, string, error) {
	_, responses := endpoint(c.BaseURL)
	if responses {
		return s.responses(ctx, c, instructions, input, schema, tools)
	}
	return s.chat(ctx, c, instructions, input, schema, tools)
}

func calculate(name, arguments string) string {
	if name != review.ToolName {
		return `{"error":"不允许调用此工具"}`
	}
	results, err := review.Calculate(json.RawMessage(arguments))
	if err != nil {
		raw, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(raw)
	}
	raw, _ := json.Marshal(map[string]any{"calculations": results})
	return string(raw)
}

func functionSpec(chat bool) map[string]any {
	spec := review.ToolSpec()
	function := map[string]any{"name": spec["name"], "description": spec["description"], "parameters": spec["inputSchema"]}
	if chat {
		return map[string]any{"type": "function", "function": function}
	}
	function["type"] = "function"
	return function
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type chatMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Refusal    string     `json:"refusal,omitempty"`
}

func (s *Service) chat(ctx context.Context, c analysis.LoginRequest, instructions, input string, schema json.RawMessage, tools bool) (analysis.Result, string, error) {
	messages := []chatMessage{{Role: "system", Content: &instructions}, {Role: "user", Content: &input}}
	for round := 0; round < maxToolRounds; round++ {
		body := map[string]any{"model": c.Model, "messages": messages, "stream": false}
		if tools {
			body["tools"] = []any{functionSpec(true)}
		}
		if schema != nil {
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "rental_extraction", "strict": true, "schema": schema}}
		}
		var response struct {
			Model   string          `json:"model"`
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Message      chatMessage `json:"message"`
				FinishReason string      `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := s.post(ctx, c, body, &response); err != nil {
			return analysis.Result{}, "", err
		}
		if hasError(response.Error) || len(response.Choices) != 1 {
			return analysis.Result{}, "", analysis.ErrFailed
		}
		choice := response.Choices[0]
		if choice.Message.Refusal != "" {
			return analysis.Result{}, "", analysis.ErrFailed
		}
		if choice.FinishReason == "tool_calls" && tools && len(choice.Message.ToolCalls) > 0 && len(choice.Message.ToolCalls) <= 64 {
			choice.Message.Role = "assistant"
			messages = append(messages, choice.Message)
			seen := make(map[string]bool)
			for _, call := range choice.Message.ToolCalls {
				if call.ID == "" || seen[call.ID] || call.Type != "function" {
					return analysis.Result{}, "", analysis.ErrFailed
				}
				seen[call.ID] = true
				output := calculate(call.Function.Name, call.Function.Arguments)
				messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: &output})
			}
			continue
		}
		if choice.FinishReason != "stop" || len(choice.Message.ToolCalls) != 0 || choice.Message.Content == nil || strings.TrimSpace(*choice.Message.Content) == "" {
			return analysis.Result{}, "", analysis.ErrFailed
		}
		model := response.Model
		if model == "" {
			model = c.Model
		}
		return analysis.Result{Text: *choice.Message.Content}, model, nil
	}
	return analysis.Result{}, "", analysis.ErrFailed
}

type responseItem struct {
	Type      string `json:"type"`
	Status    string `json:"status"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Annotations []struct {
			Type       string `json:"type"`
			Title      string `json:"title"`
			URL        string `json:"url"`
			StartIndex int    `json:"start_index"`
			EndIndex   int    `json:"end_index"`
		} `json:"annotations"`
	} `json:"content"`
}

func (s *Service) responses(ctx context.Context, c analysis.LoginRequest, instructions, input string, schema json.RawMessage, tools bool) (analysis.Result, string, error) {
	items := []any{map[string]string{"role": "user", "content": input}}
	for round := 0; round < maxToolRounds; round++ {
		body := map[string]any{"model": c.Model, "instructions": instructions, "input": items, "stream": false, "store": false}
		if tools {
			body["tools"] = []any{map[string]string{"type": "web_search"}, functionSpec(false)}
		}
		if schema != nil {
			body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "rental_extraction", "strict": true, "schema": schema}}
		}
		var response struct {
			Model  string            `json:"model"`
			Status string            `json:"status"`
			Error  json.RawMessage   `json:"error"`
			Output []json.RawMessage `json:"output"`
		}
		if err := s.post(ctx, c, body, &response); err != nil {
			return analysis.Result{}, "", err
		}
		if response.Status != "completed" || hasError(response.Error) {
			return analysis.Result{}, "", analysis.ErrFailed
		}
		var texts []string
		var citations []analysis.Citation
		textOffset := 0
		var outputs []any
		seen := make(map[string]bool)
		for _, raw := range response.Output {
			var item responseItem
			if json.Unmarshal(raw, &item) != nil {
				return analysis.Result{}, "", analysis.ErrFailed
			}
			if item.Type == "function_call" {
				if !tools || item.CallID == "" || seen[item.CallID] || len(outputs) >= 64 {
					return analysis.Result{}, "", analysis.ErrFailed
				}
				seen[item.CallID] = true
				outputs = append(outputs, map[string]string{"type": "function_call_output", "call_id": item.CallID, "output": calculate(item.Name, item.Arguments)})
			}
			if item.Type == "message" {
				if item.Status != "" && item.Status != "completed" {
					return analysis.Result{}, "", analysis.ErrFailed
				}
				for _, content := range item.Content {
					if content.Type == "refusal" {
						return analysis.Result{}, "", analysis.ErrFailed
					}
					if content.Type == "output_text" {
						length := utf8.RuneCountInString(content.Text)
						if tools {
							for _, citation := range content.Annotations {
								if citation.Type == "url_citation" && citation.StartIndex >= 0 && citation.EndIndex > citation.StartIndex && citation.EndIndex <= length {
									citations = append(citations, analysis.Citation{Title: citation.Title, URL: citation.URL, StartIndex: textOffset + citation.StartIndex, EndIndex: textOffset + citation.EndIndex})
								}
							}
						}
						texts = append(texts, content.Text)
						textOffset += length + 2 // Text parts are joined with two newlines.
					}
				}
			}
		}
		if len(outputs) > 0 {
			for _, raw := range response.Output {
				items = append(items, raw)
			}
			items = append(items, outputs...)
			continue
		}
		text := strings.Join(texts, "\n\n")
		if strings.TrimSpace(text) == "" {
			return analysis.Result{}, "", analysis.ErrFailed
		}
		model := response.Model
		if model == "" {
			model = c.Model
		}
		return analysis.Result{Text: text, Citations: citations}, model, nil
	}
	return analysis.Result{}, "", analysis.ErrFailed
}

func hasError(raw json.RawMessage) bool { return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) }
