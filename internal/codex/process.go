// Package codex connects to the official Codex app-server over stdio JSON-RPC.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type client struct {
	process childProcess
	in      io.WriteCloser
	done    chan struct{}
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan message
	next    atomic.Int64
	notify  func(message)
}

// Kill stops the managed process group on Windows, not just its root process.
type childProcess interface {
	Kill() error
	Wait() error
}

// Resolve Windows npm's native binary, avoiding shell command interpolation.
func executable(configured string) (string, error) {
	if configured != "" {
		return exec.LookPath(configured)
	}
	name, err := exec.LookPath("codex")
	if err == nil && (runtime.GOOS != "windows" || strings.EqualFold(filepath.Ext(name), ".exe")) {
		return name, nil
	}
	if runtime.GOOS == "windows" {
		shim, shimErr := exec.LookPath("codex.cmd")
		if shimErr == nil {
			arch := "x64"
			target := "x86_64-pc-windows-msvc"
			if runtime.GOARCH == "arm64" {
				arch = "arm64"
				target = "aarch64-pc-windows-msvc"
			}
			root := filepath.Join(filepath.Dir(shim), "node_modules", "@openai", "codex")
			for _, vendor := range []string{filepath.Join(root, "node_modules", "@openai", "codex-win32-"+arch, "vendor"), filepath.Join(root, "vendor")} {
				candidate := filepath.Join(vendor, target, "bin", "codex.exe")
				if _, e := os.Stat(candidate); e == nil {
					return candidate, nil
				}
			}
		}
	}
	return "", analysis.ErrUnavailable
}

func start(binary, home, cwd string, notify func(message)) (*client, error) {
	path, err := executable(binary)
	if err != nil {
		return nil, analysis.ErrUnavailable
	}
	cmd := exec.Command(path, "app-server", "--listen", "stdio://",
		"-c", `cli_auth_credentials_store="file"`, "-c", `history.persistence="none"`,
		"-c", "features.shell_tool=false", "-c", "features.apply_patch_freeform=false",
		"-c", "features.multi_agent=false", "-c", `web_search="disabled"`,
		"-c", "apps._default.enabled=false", "-c", "project_doc_max_bytes=0")
	cmd.Dir = cwd
	// Never inherit another Codex session's credentials or thread context.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "OPENAI_") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+home)
	input, in, err := os.Pipe()
	if err != nil {
		return nil, analysis.ErrUnavailable
	}
	defer input.Close()
	out, output, err := os.Pipe()
	if err != nil {
		in.Close()
		return nil, analysis.ErrUnavailable
	}
	defer output.Close()
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		in.Close()
		out.Close()
		return nil, analysis.ErrUnavailable
	}
	defer stderr.Close() // Do not log prompts, tokens or upstream errors.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, stderr
	process, err := launchProcess(cmd)
	if err != nil {
		in.Close()
		out.Close()
		return nil, analysis.ErrUnavailable
	}
	c := &client{process: process, in: in, done: make(chan struct{}), pending: make(map[string]chan message), notify: notify}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		_ = process.Wait()
		_ = process.Kill() // Reap descendants even if the root exits by itself.
	}()
	go func() {
		defer close(c.done)
		defer in.Close()
		defer out.Close()
		defer func() { _ = process.Kill(); <-exited }()
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 64*1024), 32<<20)
		for scanner.Scan() {
			var m message
			if json.Unmarshal(scanner.Bytes(), &m) != nil {
				continue
			}
			if len(m.ID) > 0 && m.Method != "" {
				_ = c.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "This application only accepts text analysis"}})
			} else if len(m.ID) > 0 {
				c.mu.Lock()
				ch := c.pending[string(m.ID)]
				c.mu.Unlock()
				if ch != nil {
					select {
					case ch <- m:
					default:
					}
				}
			} else if m.Method != "" {
				c.notify(m)
			}
		}
	}()
	return c, nil
}

func (c *client) send(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return json.NewEncoder(c.in).Encode(v)
}

func (c *client) call(ctx context.Context, method string, params any, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := c.next.Add(1)
	key, _ := json.Marshal(id)
	ch := make(chan message, 1)
	c.mu.Lock()
	c.pending[string(key)] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, string(key)); c.mu.Unlock() }()
	written := make(chan error, 1)
	go func() { written <- c.send(map[string]any{"id": id, "method": method, "params": params}) }()
	select {
	case <-ctx.Done():
		c.close() // Closing stdin also unblocks a process that stopped reading.
		return ctx.Err()
	case err := <-written:
		if err != nil {
			return analysis.ErrUnavailable
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return analysis.ErrUnavailable
	case m := <-ch:
		if m.Error != nil {
			return analysis.ErrFailed
		}
		if result != nil && json.Unmarshal(m.Result, result) != nil {
			return analysis.ErrFailed
		}
		return nil
	}
}

func (c *client) close() {
	_ = c.in.Close()
	select {
	case <-c.done:
	case <-time.After(time.Second):
		_ = c.process.Kill()
		<-c.done
	}
}

func isDone(c *client) bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func canceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
