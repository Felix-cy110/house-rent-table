package codex

import (
	"encoding/json"
	"os"
)

// Custom providers are configured for the standard Responses API. Built-in
// metadata for some models selects Responses Lite instead: it puts tools in
// input[].additional_tools and omits hosted web search. Compatible gateways can
// accept that request but silently lose the tools. Keep the selected model name
// and use an app-owned catalog with standard Responses tool serialization.
func standardResponsesCatalog(dir, model string) (string, error) {
	catalog := map[string]any{"models": []any{map[string]any{
		"slug": model, "display_name": model, "description": nil,
		"supported_reasoning_levels": []any{}, "shell_type": "disabled",
		"visibility": "list", "supported_in_api": true, "priority": 0,
		"support_verbosity": false, "supports_reasoning_summary_parameter": false,
		"truncation_policy":            map[string]any{"mode": "bytes", "limit": 10000},
		"experimental_supported_tools": []any{}, "input_modalities": []string{"text"},
		"use_responses_lite": false, "tool_mode": "direct", "web_search_tool_type": "text",
		"base_instructions": "Follow the application instructions. Use only the provided tools to review rental fees or extract the requested data.",
	}}}
	file, err := os.CreateTemp(dir, ".responses-model-*.json")
	if err != nil {
		return "", err
	}
	path := file.Name()
	err = json.NewEncoder(file).Encode(catalog)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
