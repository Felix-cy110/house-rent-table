// Package aiclient calls user-configured OpenAI-compatible APIs directly.
package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/extraction"
	"github.com/Felix-cy110/house-rent-table/skills"
)

const configFile = "connection.json"

type Options struct {
	ConfigDir            string
	HTTPClient           *http.Client
	RentPremiumTolerance *int
}

type Service struct {
	opts       Options
	client     *http.Client
	gate       chan struct{}
	mu         sync.Mutex
	loaded     bool
	connection *analysis.LoginRequest
}

func New(opts Options) *Service {
	client := http.Client{Timeout: 5 * time.Minute}
	if opts.HTTPClient != nil {
		client = *opts.HTTPClient
	}
	// Never forward an API key to a redirected host or endpoint.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Service{opts: opts, client: &client, gate: make(chan struct{}, 1)}
}

func (s *Service) path() (string, error) {
	dir := s.opts.ConfigDir
	if dir == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return "", analysis.ErrUnavailable
		}
		dir = filepath.Join(root, "house-rent-table", "api")
	}
	return filepath.Join(dir, configFile), nil
}

// Caller holds mu. A configuration is published only after a successful probe
// and atomic file replacement; readers never observe a partially written key.
func (s *Service) load() error {
	if s.loaded {
		return nil
	}
	path, err := s.path()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		s.loaded = true
		return nil
	}
	if err != nil {
		return analysis.ErrUnavailable
	}
	defer f.Close()
	var connection analysis.LoginRequest
	d := json.NewDecoder(io.LimitReader(f, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&connection) != nil || d.Decode(new(any)) != io.EOF || connection.Validate() != nil {
		return analysis.ErrUnavailable
	}
	s.connection, s.loaded = &connection, true
	return nil
}

func (s *Service) snapshot() (analysis.LoginRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return analysis.LoginRequest{}, err
	}
	if s.connection == nil {
		return analysis.LoginRequest{}, analysis.ErrLoginRequired
	}
	return *s.connection, nil
}

func (s *Service) Account(ctx context.Context) (analysis.Account, error) {
	if err := ctx.Err(); err != nil {
		return analysis.Account{}, err
	}
	c, err := s.snapshot()
	if errors.Is(err, analysis.ErrLoginRequired) {
		return analysis.Account{}, nil
	}
	if err != nil {
		return analysis.Account{}, err
	}
	return analysis.Account{Connected: true, BaseURL: c.BaseURL, Model: c.Model}, nil
}

func (s *Service) acquire() bool {
	select {
	case s.gate <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) Login(ctx context.Context, input analysis.LoginRequest) (analysis.Login, error) {
	if err := input.Validate(); err != nil {
		return analysis.Login{}, err
	}
	if !s.acquire() {
		return analysis.Login{}, analysis.ErrBusy
	}
	defer func() { <-s.gate }()
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _, err := s.complete(probe, input, "This is a connection check. Reply only with OK. Do not call tools.", "Reply with OK.", nil, false)
	if err != nil {
		message := "连接验证失败，请检查 API URL、模型、Key 和接口兼容性后重试"
		var upstream *apiError
		if errors.As(err, &upstream) {
			message = upstream.Error()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			message = "连接验证超时，请检查 API URL、网络和供应商服务后重试"
		}
		if errors.Is(err, context.Canceled) {
			message = "连接验证已取消，请重试"
		}
		return analysis.Login{}, &analysis.ConnectionError{Message: message, Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return analysis.Login{}, err
	}
	path, err := s.path()
	if err != nil {
		return analysis.Login{}, err
	}
	data, _ := json.Marshal(input)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := saveConfig(path, data); err != nil {
		return analysis.Login{}, analysis.ErrUnavailable
	}
	s.connection, s.loaded = &input, true
	return analysis.Login{OK: true}, nil
}

func saveConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".connection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (s *Service) Logout(ctx context.Context) error {
	if !s.acquire() {
		return analysis.ErrBusy
	}
	defer func() { <-s.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path()
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return analysis.ErrUnavailable
	}
	s.connection, s.loaded = nil, true
	return nil
}

func (s *Service) Analyze(ctx context.Context, payload json.RawMessage) (analysis.Result, error) {
	if !s.acquire() {
		return analysis.Result{}, analysis.ErrBusy
	}
	defer func() { <-s.gate }()
	c, err := s.snapshot()
	if err != nil {
		return analysis.Result{}, err
	}
	tolerance := 20
	if s.opts.RentPremiumTolerance != nil {
		tolerance = *s.opts.RentPremiumTolerance
	}
	guidance := fmt.Sprintf("%s\n本次租金溢价容忍值：%d%%（产品提示规则，不是市场或法律标准）。本次查询日期：%s。", skills.Instructions(), tolerance, time.Now().Format("2006-01-02"))
	_, responses := endpoint(c.BaseURL)
	if !responses {
		guidance += "\n本次 API 接口未提供实时网页搜索工具。不得声称已联网核价，不得编造来源或当前市价；核价部分明确写‘未能核实：当前接口未提供联网搜索’，仍完成表内费用核算和收费矛盾检查。"
	}
	result, _, err := s.complete(ctx, c, guidance, string(payload), nil, true)
	return result, err
}

// Extract snapshots the connection independently of the main report. Its
// messages, structured output and cancellation never share the report session.
func (s *Service) Extract(ctx context.Context, input extraction.Input) (extraction.Output, error) {
	c, err := s.snapshot()
	if err != nil {
		return extraction.Output{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return extraction.Output{}, analysis.ErrFailed
	}
	result, model, err := s.complete(ctx, c, extraction.Instructions(), string(raw), extraction.Schema(), false)
	u, _ := url.Parse(c.BaseURL)
	return extraction.Output{JSON: json.RawMessage(result.Text), Provider: u.Host, Model: model}, err
}

// A base URL uses Chat Completions. A complete /responses URL explicitly opts
// into Responses and its hosted web search; no second paid request is guessed.
func endpoint(base string) (string, bool) {
	if strings.HasSuffix(base, "/responses") {
		return base, true
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base, false
	}
	return base + "/chat/completions", false
}
