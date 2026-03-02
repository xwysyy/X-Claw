//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func prepareCommandForTermination(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	// Create an isolated process group for the spawned shell command so timeout kills
	// can take down the whole tree without touching the parent.
	//
	// NOTE: Setsid is intentionally not used because some restricted environments
	// (e.g., certain CI sandboxes) may block it with EPERM.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}

	// Kill the entire process group spawned by the shell command.
	// This is the most reliable way to stop background children started via "&".
	//
	// Safety guard: never send to pid=-1 (signals *all* processes) or our own process group.
	ownPgrp := syscall.Getpgrp()
	if pid > 1 && pid != ownPgrp {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}

	// Fallback kill on the shell process itself.
	_ = cmd.Process.Kill()
	return nil
}
