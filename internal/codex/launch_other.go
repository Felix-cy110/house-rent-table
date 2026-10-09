//go:build !windows

package codex

import "os/exec"

type commandProcess struct{ cmd *exec.Cmd }

func launchProcess(cmd *exec.Cmd) (childProcess, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &commandProcess{cmd: cmd}, nil
}

func (p *commandProcess) Kill() error { return p.cmd.Process.Kill() }
func (p *commandProcess) Wait() error { return p.cmd.Wait() }
