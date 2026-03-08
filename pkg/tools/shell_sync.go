package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

func (t *ExecTool) executeDockerSync(ctx context.Context, command, cwd string, timeout time.Duration) *ToolResult {
	image := strings.TrimSpace(t.dockerImage)
	if image == "" {
		return ErrorResult("docker backend selected but tools.exec.docker.image is empty")
	}
	workspace := strings.TrimSpace(t.workingDir)
	if workspace == "" {
		return ErrorResult("docker backend requires a non-empty workingDir")
	}

	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	containerWD := "/workspace"
	if strings.TrimSpace(cwd) != "" && workspace != "" {
		if rel, err := filepath.Rel(workspace, cwd); err == nil {
			rel = strings.TrimSpace(rel)
			if rel != "" && rel != "." && !strings.HasPrefix(rel, "..") {
				containerWD = filepath.ToSlash(filepath.Join(containerWD, rel))
			}
		}
	}

	network := strings.TrimSpace(t.dockerNetwork)
	if network == "" {
		network = "none"
	}

	args := []string{"run", "--rm", "--init", "--network", network, "-v", fmt.Sprintf("%s:/workspace", workspace), "-w", containerWD}
	if t.dockerMemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", t.dockerMemoryMB))
	}
	if t.dockerCPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%g", t.dockerCPUs))
	}
	if t.dockerPidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", t.dockerPidsLimit))
	}
	if t.dockerReadOnlyRootFS {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,size=64m", "--tmpfs", "/var/tmp:rw,size=64m")
	}
	args = append(args, image, "sh", "-lc", command)

	cmd := exec.CommandContext(cmdCtx, "docker", args...)
	prepareCommandForTermination(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return ErrorResult(fmt.Sprintf("failed to start docker sandbox: %v", err))
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	select {
	case err = <-done:
	case <-cmdCtx.Done():
		if termErr := terminateProcessTree(cmd); termErr != nil {
			logger.DebugCF("tools/shell", "terminate process tree failed", map[string]any{"error": termErr.Error()})
		}
		select {
		case err = <-done:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			err = <-done
		}
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			msg := fmt.Sprintf("Command timed out after %v", timeout)
			return &ToolResult{ForLLM: msg, ForUser: msg, IsError: true}
		}
		output += fmt.Sprintf("\nExit code: %v", err)
	}

	output = truncateExecOutput(output)
	if err != nil {
		return &ToolResult{ForLLM: output, ForUser: output, IsError: true}
	}
	return &ToolResult{ForLLM: output, ForUser: output, IsError: false}
}

func (t *ExecTool) executeSync(ctx context.Context, command, cwd string, timeout time.Duration) *ToolResult {
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := shellCommand(cmdCtx, t.withHostLimits(command))
	cmd.Dir = cwd
	t.applyEnvPolicy(cmd)
	prepareCommandForTermination(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	select {
	case err = <-done:
	case <-cmdCtx.Done():
		if termErr := terminateProcessTree(cmd); termErr != nil {
			logger.DebugCF("tools/shell", "terminate process tree failed", map[string]any{"error": termErr.Error()})
		}
		select {
		case err = <-done:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			err = <-done
		}
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			msg := fmt.Sprintf("Command timed out after %v", timeout)
			if strings.TrimSpace(output) != "" {
				msg = truncateExecOutput(output) + "\n" + msg
			}
			return &ToolResult{ForLLM: msg, ForUser: msg, IsError: true}
		}
		if output != "" {
			output += "\n"
		}
		output += fmt.Sprintf("Exit code: %v", err)
	}

	output = truncateExecOutput(output)
	if err != nil {
		return &ToolResult{ForLLM: output, ForUser: output, IsError: true}
	}
	return &ToolResult{ForLLM: output, ForUser: output, IsError: false}
}
