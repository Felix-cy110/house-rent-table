//go:build windows

package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Helpers are dedicated test processes. In particular, the parent-death test
// kills this helper, never the real web server or the user's installed Codex.
func init() {
	if len(os.Args) < 2 {
		return
	}
	var err error
	switch {
	case os.Args[1] == "job-test-parent":
		dir := os.Args[2]
		_ = os.Setenv("RENT_JOB_TEST_DIR", dir)
		_ = os.Setenv("RENT_JOB_TEST_MODE", os.Args[3])
		err = jobParent(dir)
	case os.Args[1] == "job-test-descendant":
		err = recordPID(os.Args[2], "descendant")
		if err == nil {
			time.Sleep(time.Minute)
		} // Ignore EOF; require OS cleanup.
	case os.Args[1] == "job-test-echo":
		cwd, e := os.Getwd()
		if e != nil {
			err = e
			break
		}
		err = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[2:], "value": os.Getenv("RENT_JOB_VALUE"), "cwd": cwd})
	case os.Args[1] == "app-server" && os.Getenv("RENT_JOB_TEST_MODE") != "":
		err = jobWorker(os.Getenv("RENT_JOB_TEST_DIR"), os.Getenv("RENT_JOB_TEST_MODE"))
	default:
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func recordPID(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name+".pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
}

func jobParent(dir string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	c, err := start(executable, dir, dir, func(message) {})
	if err != nil {
		return err
	}
	defer c.close()
	_, err = io.Copy(io.Discard, os.Stdin) // Test holds stdin open until killing us.
	return err
}

func jobWorker(dir, mode string) error {
	if err := recordPID(dir, "root"); err != nil {
		return err
	}
	if mode == "starting" {
		time.Sleep(time.Minute)
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command(executable, "job-test-descendant", dir)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr // Keep the pipe open after root exit.
	if err := child.Start(); err != nil {
		return err
	}
	defer child.Process.Release()
	switch mode {
	case "polite":
		_, err = io.Copy(io.Discard, os.Stdin)
		return err
	case "root-exit":
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			if _, err := os.Stat(filepath.Join(dir, "exit-root")); err == nil {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		return fmt.Errorf("root exit signal not received")
	default:
		time.Sleep(time.Minute)
		return nil
	}
}

func processHandle(t *testing.T, dir, name string) windows.Handle {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		contents, err := os.ReadFile(filepath.Join(dir, name+".pid"))
		if err == nil {
			pid, err := strconv.ParseUint(string(contents), 10, 32)
			if err == nil {
				handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
				if err != nil {
					t.Fatalf("open %s process: %v", name, err)
				}
				t.Cleanup(func() { _ = windows.TerminateProcess(handle, 1); _ = windows.CloseHandle(handle) })
				return handle
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s process did not report its PID", name)
	return 0
}

func requireExited(t *testing.T, handle windows.Handle) {
	t.Helper()
	status, err := windows.WaitForSingleObject(handle, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("process survived cleanup: status=%d err=%v", status, err)
	}
}

func workerClient(t *testing.T, mode string) (*client, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RENT_JOB_TEST_DIR", dir)
	t.Setenv("RENT_JOB_TEST_MODE", mode)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c, err := start(executable, dir, dir, func(message) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c, dir
}

func TestWindowsJobClosesWholeTree(t *testing.T) {
	for _, mode := range []string{"polite", "stubborn"} {
		t.Run(mode, func(t *testing.T) {
			c, dir := workerClient(t, mode)
			root := processHandle(t, dir, "root")
			descendant := processHandle(t, dir, "descendant")
			// Simultaneous timeout, reader and owner cleanup must be idempotent.
			var closing sync.WaitGroup
			for i := 0; i < 3; i++ {
				closing.Add(1)
				go func() { defer closing.Done(); c.close() }()
			}
			closing.Wait()
			requireExited(t, root)
			requireExited(t, descendant)
		})
	}
}

func TestWindowsJobKillsTreeWhenParentIsTerminated(t *testing.T) {
	for _, mode := range []string{"starting", "stubborn"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			parent := exec.Command(executable, "job-test-parent", dir, mode)
			stdin, err := parent.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
			root := processHandle(t, dir, "root")
			var descendant windows.Handle
			if mode != "starting" {
				descendant = processHandle(t, dir, "descendant")
			}
			// Terminate only the parent. No defer, EOF cooperation or tree-kill
			// command is available to it; Windows must enforce the Job rule.
			if err := parent.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = parent.Wait()
			requireExited(t, root)
			if descendant != 0 {
				requireExited(t, descendant)
			}
		})
	}
}

func TestWindowsRootExitReapsDescendantHoldingStdout(t *testing.T) {
	c, dir := workerClient(t, "root-exit")
	root := processHandle(t, dir, "root")
	descendant := processHandle(t, dir, "descendant")
	if err := os.WriteFile(filepath.Join(dir, "exit-root"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("descendant kept stdout and client alive after root exited")
	}
	requireExited(t, root)
	requireExited(t, descendant)
}

func TestWindowsLaunchPreservesArgumentsEnvironmentAndDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 含空格目录")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"", "中文 空格", `a"b`, `尾斜杠\`, `$(literal)`, "line\nbreak"}
	cmd := exec.Command(executable, append([]string{"job-test-echo"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "RENT_JOB_VALUE=中文\n原样值")
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	output, err := os.CreateTemp(dir, "output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, output, null
	process, err := launchProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Kill()
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var echo struct {
		Args  []string `json:"args"`
		Value string   `json:"value"`
		Cwd   string   `json:"cwd"`
	}
	if err := json.NewDecoder(output).Decode(&echo); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(echo.Args, args) || echo.Value != "中文\n原样值" || !strings.EqualFold(echo.Cwd, dir) {
		t.Fatalf("launch data changed: %+v", echo)
	}
}

func TestWindowsLaunchFailureReleasesHandles(t *testing.T) {
	// Keep the native-handle measurement independent of Go expanding its worker
	// pool during blocking syscalls. This test must not run in parallel.
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	countProc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	count := func() uint32 {
		var value uint32
		ok, _, err := countProc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&value)))
		if ok == 0 {
			t.Fatal(err)
		}
		return value
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing.exe"))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	failedStart := func() {
		t.Helper()
		if process, err := launchProcess(cmd); err == nil {
			_ = process.Kill()
			_ = process.Wait()
			t.Fatal("missing executable accepted")
		}
	}
	// Go creates worker-thread/event handles lazily. Warm up both launch and GC
	// before measuring; a single launch does not finish runtime initialization.
	for i := 0; i < 25; i++ {
		failedStart()
	}
	runtime.GC()
	before := count()
	for i := 0; i < 100; i++ {
		failedStart()
	}
	// Allow a few runtime handles, while detecting even one leaked handle per
	// failure (100 attempts would then add at least 100 handles).
	if after := count(); after > before+8 {
		t.Fatalf("failed starts leaked handles: before=%d after=%d", before, after)
	}
	if _, err := environmentBlock([]string{"BROKEN=value\x00suffix"}); err == nil {
		t.Fatal("embedded NUL accepted")
	}
	// Invalid streams fail before creation and must also release partial setup.
	cmd.Stderr = &bytes.Buffer{}
	if _, err := launchProcess(cmd); err == nil {
		t.Fatal("unsupported stream accepted")
	}
}

// Isolated, opt-in native executable check: no login, OAuth callback or model
// request, and no use of the user's normal Codex configuration directory.
func TestInstalledCodexWindowsJob(t *testing.T) {
	if os.Getenv("RENT_CODEX_JOB_SMOKE") != "1" {
		t.Skip("set RENT_CODEX_JOB_SMOKE=1 to verify the installed CLI in a Job")
	}
	s := New(Options{Home: t.TempDir()})
	t.Cleanup(s.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	account, err := s.Account(ctx)
	if err != nil || account.LoggedIn {
		t.Fatalf("isolated native Codex handshake: account=%+v err=%v", account, err)
	}
	c, err := s.connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	process, ok := c.process.(*jobProcess)
	if !ok {
		t.Fatal("native Codex is not managed by a Job")
	}
	var handle windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), process.process, windows.CurrentProcess(), &handle, windows.SYNCHRONIZE, false, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	s.Close()
	requireExited(t, handle)
}
