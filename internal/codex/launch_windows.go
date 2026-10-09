//go:build windows

package codex

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PROC_THREAD_ATTRIBUTE_JOB_LIST: ProcThreadAttributeValue(13, false, true, false).
// Windows 10+ associates the job as part of process creation, before any child
// code runs. Starting first and calling AssignProcessToJobObject leaves a gap
// in which terminating the parent could leave an unowned child behind.
const procThreadAttributeJobList = 0x0002000D

type jobProcess struct {
	process  windows.Handle
	job      windows.Handle
	killOnce sync.Once
	killErr  error
}

func launchProcess(cmd *exec.Cmd) (childProcess, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	path, err := filepath.Abs(cmd.Path)
	if err != nil {
		return nil, err
	}
	application, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return nil, err
	}
	var directory *uint16
	if cmd.Dir != "" {
		directory, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}
	environment, err := environmentBlock(cmd.Environ())
	if err != nil {
		return nil, err
	}

	// An unnamed job with a non-inheritable handle. This parent is its only
	// handle owner; the child belongs to the job without owning a job handle.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create Codex job: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			_ = windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, fmt.Errorf("configure Codex job: %w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	if err := attributes.Update(procThreadAttributeJobList, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, fmt.Errorf("bind Codex job at creation: %w", err)
	}

	// Only stdin/stdout/stderr are inherited. In particular, neither the Job
	// handle nor unrelated handles from the Go server reach the child.
	var handles [3]windows.Handle
	defer func() {
		for _, handle := range handles {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
		}
	}()
	for i, stream := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
		file, ok := stream.(*os.File)
		if !ok || file == nil {
			return nil, errors.New("Codex stdio must use files")
		}
		if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &handles[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
	}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), unsafe.Sizeof(handles)); err != nil {
		return nil, err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW
	startup.ShowWindow = windows.SW_HIDE
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[1], handles[2]
	startup.ProcThreadAttributeList = attributes.List()
	var info windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NO_WINDOW)
	err = windows.CreateProcess(application, commandLine, nil, nil, true, flags, &environment[0], directory, &startup.StartupInfo, &info)
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(cmd)
	if err != nil {
		// No fallback to an uncontained process if the host's Job policy or
		// Windows version prevents association.
		return nil, fmt.Errorf("start Codex in job: %w", err)
	}
	_ = windows.CloseHandle(info.Thread)
	owned = false
	return &jobProcess{process: info.Process, job: job}, nil
}

func (p *jobProcess) Kill() error {
	// Closing the only Job handle terminates its entire process tree, including
	// descendants keeping stdout open. Concurrent cleanup paths close it once.
	p.killOnce.Do(func() { p.killErr = windows.CloseHandle(p.job) })
	return p.killErr
}

func (p *jobProcess) Wait() error {
	defer windows.CloseHandle(p.process)
	status, err := windows.WaitForSingleObject(p.process, windows.INFINITE)
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("unexpected process wait status: %d", status)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("Codex exited with code %d", code)
	}
	return nil
}

func environmentBlock(env []string) ([]uint16, error) {
	entries := append([]string(nil), env...)
	for _, entry := range entries {
		if strings.ContainsRune(entry, '\x00') {
			return nil, errors.New("environment contains NUL")
		}
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToUpper(entries[i]) < strings.ToUpper(entries[j]) })
	return utf16.Encode([]rune(strings.Join(entries, "\x00") + "\x00\x00")), nil
}
