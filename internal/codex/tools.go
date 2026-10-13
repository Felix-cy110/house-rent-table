package codex

import (
	"encoding/json"

	"github.com/Felix-cy110/house-rent-table/internal/review"
)

// Only the active review may call the bounded, local arithmetic tool. Other
// server requests (including approvals) are rejected in the transport layer.
func (s *Service) toolRequest(m message) any {
	var call struct {
		ThreadID  string          `json:"threadId"`
		Tool      string          `json:"tool"`
		Namespace string          `json:"namespace"`
		Arguments json.RawMessage `json:"arguments"`
	}
	s.mu.Lock()
	valid := json.Unmarshal(m.Params, &call) == nil && call.ThreadID != "" && call.ThreadID == s.threadID && s.runContext != nil && s.runContext.Err() == nil
	s.mu.Unlock()
	if !valid || call.Tool != review.ToolName || call.Namespace != "" {
		return toolResponse(false, "当前请求不允许调用此工具")
	}
	results, err := review.Calculate(call.Arguments)
	if err != nil {
		return toolResponse(false, err.Error())
	}
	success := true
	for _, result := range results {
		if result.Error != "" {
			success = false
		}
	}
	data, _ := json.Marshal(map[string]any{"calculations": results})
	return toolResponse(success, string(data))
}

func toolResponse(success bool, text string) any {
	return map[string]any{"success": success, "contentItems": []any{map[string]string{"type": "inputText", "text": text}}}
}
