package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

// A recorder belongs to one foreground analysis. Probes and background
// extraction never inherit it, even when they use the same Service.
type turnContextKey struct{}

type turnLog struct {
	SchemaVersion int             `json:"schemaVersion"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	StartedAt     time.Time       `json:"startedAt"`
	CompletedAt   time.Time       `json:"completedAt"`
	DurationMS    int64           `json:"durationMs"`
	Status        Finish          `json:"status"`
	Input         json.RawMessage `json:"input"`
	Events        []*turnEvent    `json:"events"`
	Usage         *Usage          `json:"usage"`
	Result        analysis.Result `json:"result"`
}

type turnEvent struct {
	Sequence     int             `json:"sequence"`
	Kind         string          `json:"kind"`
	StartedAt    time.Time       `json:"startedAt"`
	DurationMS   int64           `json:"durationMs"`
	Method       string          `json:"method,omitempty"`
	URL          string          `json:"url,omitempty"`
	Request      json.RawMessage `json:"request,omitempty"`
	HTTPStatus   int             `json:"httpStatus,omitempty"`
	Response     json.RawMessage `json:"response,omitempty"`
	ResponseText string          `json:"responseText,omitempty"`
	Truncated    bool            `json:"truncated,omitempty"`
	Error        string          `json:"error,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	Name         string          `json:"name,omitempty"`
	Arguments    string          `json:"arguments,omitempty"`
	Output       string          `json:"output,omitempty"`
}

func startTurnEvent(ctx context.Context, kind string) *turnEvent {
	turn, _ := ctx.Value(turnContextKey{}).(*turnLog)
	if turn == nil {
		return nil
	}
	event := &turnEvent{Sequence: len(turn.Events) + 1, Kind: kind, StartedAt: time.Now().UTC()}
	turn.Events = append(turn.Events, event)
	return event
}

func (e *turnEvent) response(raw []byte, limit int64) {
	if e == nil {
		return
	}
	if int64(len(raw)) > limit {
		raw, e.Truncated = raw[:limit], true
	}
	if json.Valid(raw) {
		e.Response = append(json.RawMessage(nil), raw...)
	} else if len(raw) > 0 {
		e.ResponseText = string(raw)
	}
}

func (e *turnEvent) finish(err error) {
	if e == nil {
		return
	}
	e.DurationMS = time.Since(e.StartedAt).Milliseconds()
	if err != nil {
		e.Error = err.Error()
	}
}

func (t *turnLog) encode(secrets ...string) string {
	raw, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	// Redact credentials even if a vendor echoes them in a response/error.
	// UseNumber preserves numeric IDs and unknown vendor fields exactly.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return ""
	}
	// Tool arguments/results may themselves contain serialized JSON strings.
	// Include JSON-escaped spellings of keys as well as their literal values.
	var variants []string
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for _, escapeHTML := range []bool{true, false} {
			var encoded bytes.Buffer
			encoder := json.NewEncoder(&encoded)
			encoder.SetEscapeHTML(escapeHTML)
			_ = encoder.Encode(secret)
			quoted := bytes.TrimSpace(encoded.Bytes())
			variants = append(variants, string(quoted[1:len(quoted)-1]))
		}
		variants = append(variants, secret)
	}
	raw, err = json.MarshalIndent(redactTurn(value, variants), "", "  ")
	if err != nil {
		return ""
	}
	return string(raw)
}

func redactTurn(value any, secrets []string) any {
	switch v := value.(type) {
	case string:
		for _, secret := range secrets {
			if secret != "" {
				v = strings.ReplaceAll(v, secret, "[REDACTED]")
			}
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = redactTurn(item, secrets)
		}
	case map[string]any:
		clean := make(map[string]any, len(v))
		for key, item := range v {
			clean[redactTurn(key, secrets).(string)] = redactTurn(item, secrets)
		}
		return clean
	}
	return value
}
