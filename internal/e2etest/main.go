//go:build e2e

// This browser-test server uses real handlers and AI orchestration with an
// in-memory transport. It cannot call vendors, load .env or connect to MySQL.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/Felix-cy110/house-rent-table/internal/aiclient"
	"github.com/Felix-cy110/house-rent-table/internal/httpapi"
)

type fixtureTransport struct{}

func (fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Model    string `json:"model"`
		Thinking struct {
			Type string `json:"type"`
		} `json:"thinking"`
		Tools    []any `json:"tools"`
		Messages []struct {
			Role, Content string
			Reasoning     string `json:"reasoning_content"`
		} `json:"messages"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.String() != "https://api.xiaomimimo.com/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer local-test-only-key" || body.Model != "mimo-v2.6-flash" || body.Thinking.Type != "enabled" {
		w.WriteHeader(400)
		return w.Result(), nil
	}
	var response string
	if len(body.Tools) == 0 {
		response = `{"choices":[{"finish_reason":"stop","message":{"content":"OK"}}]}`
	} else if len(body.Messages) == 2 && strings.Contains(body.Messages[1].Content, "月租") {
		response = `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_content":"fixture-private-thinking","tool_calls":[{"id":"calc1","type":"function","function":{"name":"rental_calculate","arguments":"{\"calculations\":[{\"label\":\"押一付三\",\"expression\":\"2000*(1+3)\"}]}"}}]}}]}`
	} else if len(body.Messages) == 4 && body.Messages[2].Reasoning == "fixture-private-thinking" && strings.Contains(body.Messages[3].Content, "8000.00") {
		response = `{"choices":[{"finish_reason":"stop","message":{"content":"押一付三合计 8000.00 元。","reasoning_content":"fixture-private-thinking","annotations":[{"type":"url_citation","title":"测试公示","url":"https://example.com/fees"}]}}]}`
	} else {
		w.WriteHeader(400)
		return w.Result(), nil
	}
	_, _ = w.Write([]byte(response))
	return w.Result(), nil
}
func main() {
	dir, err := os.MkdirTemp("", "house-rent-e2e-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	agent := aiclient.New(aiclient.Options{ConfigDir: dir, HTTPClient: &http.Client{Transport: fixtureTransport{}}})
	templates, err := filepath.Glob("outputs/*/租房信息模板.xlsx")
	if err != nil || len(templates) != 1 {
		log.Fatal("test template unavailable")
	}
	server := httpapi.New(httpapi.Options{Analyzer: agent, Auth: agent, WebDir: "web/dist", LocalOnly: true, TemplatePath: templates[0]})
	log.Fatal(http.ListenAndServe("127.0.0.1:18080", server))
}
