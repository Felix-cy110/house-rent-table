package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolRequestsAreScopedToTheActiveReview(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Options{})
	s.threadID, s.runContext = "active", ctx
	for _, tc := range []struct {
		name, thread, tool, namespace, expression string
		success                                   bool
	}{
		{"valid", "active", "rental_calculate", "", "0.1+0.2", true},
		{"other thread", "other", "rental_calculate", "", "1+1", false},
		{"unknown tool", "active", "shell", "", "1+1", false},
		{"unknown namespace", "active", "rental_calculate", "other", "1+1", false},
		{"invalid expression", "active", "rental_calculate", "", "1/0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params, _ := json.Marshal(map[string]any{"threadId": tc.thread, "tool": tc.tool, "namespace": tc.namespace, "arguments": map[string]any{"calculations": []any{map[string]string{"label": "test", "expression": tc.expression}}}})
			result := s.toolRequest(message{Params: params}).(map[string]any)
			if result["success"] != tc.success {
				t.Fatalf("%+v", result)
			}
		})
	}
	cancel()
	result := s.toolRequest(message{Params: json.RawMessage(`{"threadId":"active","tool":"rental_calculate","arguments":{"calculations":[{"label":"late","expression":"1+1"}]}}`)}).(map[string]any)
	if result["success"] != false {
		t.Fatal("canceled review accepted a calculation")
	}
}

func TestConfiguredTolerance(t *testing.T) {
	for _, tolerance := range []int{0, 5, 20, 100} {
		s := New(Options{RentPremiumTolerance: &tolerance, Model: "test-model"})
		params := s.threadParams()
		if params["model"] != "test-model" || !strings.Contains(params["developerInstructions"].(string), "产品提示规则") {
			t.Fatal("missing model or review guidance")
		}
		// Zero is a deliberate tolerance, not a request for the default.
		if tolerance == 0 && !strings.Contains(params["developerInstructions"].(string), "：0%") {
			t.Fatal("zero tolerance lost")
		}
	}
}
