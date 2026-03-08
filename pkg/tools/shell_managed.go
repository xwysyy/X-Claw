package tools

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

func (t *ExecTool) executeManaged(ctx context.Context, command, cwd string, background bool, yield time.Duration, timeout time.Duration) *ToolResult {
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}

	cmd := shellCommand(cmdCtx, t.withHostLimits(command))
	cmd.Dir = cwd
	t.applyEnvPolicy(cmd)
	prepareCommandForTermination(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return ErrorResult(fmt.Sprintf("failed to attach stdout: %v", err))
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return ErrorResult(fmt.Sprintf("failed to attach stderr: %v", err))
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return ErrorResult(fmt.Sprintf("failed to attach stdin: %v", err))
	}

	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdinPipe.Close()
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	sessionID := t.processes.StartSession(command, cwd, cmd, stdinPipe, cancel)
	done := make(chan struct{}, 1)
	go t.watchManagedCommand(sessionID, cmdCtx, cmd, stdoutPipe, stderrPipe, done)

	if background || yield <= 0 {
		return t.runningSessionResult(sessionID)
	}

	result, err := t.processes.Poll(sessionID, yield)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if result.Session.Status == "running" || result.TimedOut {
		return t.runningSessionResult(sessionID)
	}
	return formatManagedCompletion(result, timeout)
}

func (t *ExecTool) watchManagedCommand(sessionID string, cmdCtx context.Context, cmd *exec.Cmd, stdoutPipe, stderrPipe io.ReadCloser, done chan<- struct{}) {
	defer func() {
		if done != nil {
			done <- struct{}{}
		}
	}()

	go func() {
		<-cmdCtx.Done()
		if cmdCtx.Err() != nil {
			_ = terminateProcessTree(cmd)
		}
	}()

	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() {
		t.streamManagedOutput(sessionID, stdoutPipe, false)
		close(stdoutDone)
	}()
	go func() {
		t.streamManagedOutput(sessionID, stderrPipe, true)
		close(stderrDone)
	}()

	waitErr := cmd.Wait()
	<-stdoutDone
	<-stderrDone
	t.processes.MarkExited(sessionID, waitErr, cmdCtx.Err() == context.DeadlineExceeded)
}

func (t *ExecTool) streamManagedOutput(sessionID string, reader io.ReadCloser, stderr bool) {
	defer func() {
		if reader != nil {
			_ = reader.Close()
		}
	}()
	if reader == nil || t == nil || t.processes == nil {
		return
	}

	buf := make([]byte, 4096)
	wroteStderrHeader := false
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			if stderr && !wroteStderrHeader {
				t.processes.AppendOutput(sessionID, "\nSTDERR:\n")
				wroteStderrHeader = true
			}
			t.processes.AppendOutput(sessionID, chunk)
		}
		if err != nil {
			return
		}
	}
}

func (t *ExecTool) runningSessionResult(sessionID string) *ToolResult {
	snapshot, ok := t.processes.GetSnapshot(sessionID)
	if !ok {
		return ErrorResult("process session not found")
	}
	return marshalSilentJSON(map[string]any{
		"session_id": sessionID,
		"session":    snapshot,
		"status":     strings.TrimSpace(snapshot.Status),
	})
}
