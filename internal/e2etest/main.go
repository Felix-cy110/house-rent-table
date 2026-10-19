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
	if r.URL.String() == "https://open.bigmodel.cn/api/paas/v4/web_search" {
		var search struct {
			Query string `json:"search_query"`
		}
		if r.Header.Get("Authorization") != "Bearer local-search-only-key" || json.NewDecoder(r.Body).Decode(&search) != nil || search.Query != "测试区域 租金" {
			w.WriteHeader(400)
			return w.Result(), nil
		}
		_, _ = w.Write([]byte(`{"search_result":[{"title":"测试公示","link":"https://example.com/fees","content":"测试区域月租 2000 元"}]}`))
		return w.Result(), nil
	}
	var body struct {
		Model    string `json:"model"`
		Thinking struct {
			Type string `json:"type"`
		} `json:"thinking"`
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
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
		if len(body.Tools) == 2 && body.Tools[1].Type == "web_search" {
			w.WriteHeader(403)
			response = `{"error":{"message":"Web Search plugin not activated"}}`
		} else {
			response = `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_content":"fixture-private-thinking","tool_calls":[{"id":"search1","type":"function","function":{"name":"rental_web_search","arguments":"{\"query\":\"测试区域 租金\"}"}}]}}]}`
		}
	} else if len(body.Messages) == 6 && body.Messages[4].Reasoning == "fixture-private-thinking" && strings.Contains(body.Messages[5].Content, "https://example.com/fees") {
		response = `{"choices":[{"finish_reason":"stop","message":{"content":"押一付三合计 8000.00 元。","reasoning_content":"fixture-private-thinking"}}]}`
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
	fallback, err := aiclient.NewSearchFallback("glm", "local-search-only-key")
	if err != nil {
		log.Fatal(err)
	}
	agent := aiclient.New(aiclient.Options{ConfigDir: dir, HTTPClient: &http.Client{Transport: fixtureTransport{}}, SearchFallback: fallback})
	templates, err := filepath.Glob("outputs/*/租房信息模板.xlsx")
	if err != nil || len(templates) != 1 {
		log.Fatal("test template unavailable")
	}
	server := httpapi.New(httpapi.Options{Analyzer: agent, Auth: agent, WebDir: "web/dist", LocalOnly: true, TemplatePath: templates[0]})
	log.Fatal(http.ListenAndServe("127.0.0.1:18080", server))
}
