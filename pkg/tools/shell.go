package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/logger"
)

type ExecTool struct {
	workingDir          string
	timeout             time.Duration
	denyPatterns        []*regexp.Regexp
	allowPatterns       []*regexp.Regexp
	customAllowPatterns []*regexp.Regexp
	restrictToWorkspace bool
	processes           *ProcessManager

	backend string // host | docker

	envMode  string
	envAllow map[string]bool

	hostMemoryMB   int
	hostCPUSeconds int
	hostFileSizeMB int
	hostNProc      int

	dockerImage          string
	dockerNetwork        string
	dockerReadOnlyRootFS bool
	dockerMemoryMB       int
	dockerCPUs           float64
	dockerPidsLimit      int
}

func NewExecTool(workingDir string, restrict bool) (*ExecTool, error) {
	return NewExecToolWithConfig(workingDir, restrict, nil)
}

func NewExecToolWithConfig(workingDir string, restrict bool, config *config.Config) (*ExecTool, error) {
	denyPatterns := make([]*regexp.Regexp, 0)
	customAllowPatterns := make([]*regexp.Regexp, 0)
	backend := "host"
	envMode := "inherit"
	envAllow := map[string]bool{}
	hostMemoryMB := 0
	hostCPUSeconds := 0
	hostFileSizeMB := 0
	hostNProc := 0
	dockerImage := ""
	dockerNetwork := ""
	dockerReadOnly := false
	dockerMemoryMB := 0
	dockerCPUs := 0.0
	dockerPidsLimit := 0

	if config != nil {
		execConfig := config.Tools.Exec
		enableDenyPatterns := execConfig.EnableDenyPatterns
		if enableDenyPatterns {
			denyPatterns = append(denyPatterns, defaultDenyPatterns...)
			if len(execConfig.CustomDenyPatterns) > 0 {
				logger.InfoCF("tools/shell", "Using custom deny patterns", map[string]any{
					"patterns": execConfig.CustomDenyPatterns,
				})
				for _, pattern := range execConfig.CustomDenyPatterns {
					re, err := regexp.Compile(pattern)
					if err != nil {
						return nil, fmt.Errorf("invalid custom deny pattern %q: %w", pattern, err)
					}
					denyPatterns = append(denyPatterns, re)
				}
			}
		} else {
			logger.WarnCF("tools/shell", "Deny patterns disabled, all commands allowed", nil)
		}
		for _, pattern := range execConfig.CustomAllowPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid custom allow pattern %q: %w", pattern, err)
			}
			customAllowPatterns = append(customAllowPatterns, re)
		}

		backend = strings.ToLower(strings.TrimSpace(execConfig.Backend))
		if backend == "" {
			backend = "host"
		}
		switch backend {
		case "host", "docker":
			// ok
		default:
			return nil, fmt.Errorf("invalid tools.exec.backend %q (expected \"host\" or \"docker\")", execConfig.Backend)
		}

		envMode = strings.ToLower(strings.TrimSpace(execConfig.Env.Mode))
		if envMode == "" {
			envMode = "inherit"
		}
		switch envMode {
		case "inherit", "allowlist":
			// ok
		default:
			return nil, fmt.Errorf("invalid tools.exec.env.mode %q (expected \"inherit\" or \"allowlist\")", execConfig.Env.Mode)
		}
		envAllow = buildExecEnvAllowMap(execConfig.Env.EnvAllow)

		hostMemoryMB = execConfig.HostLimits.MemoryMB
		hostCPUSeconds = execConfig.HostLimits.CPUSeconds
		hostFileSizeMB = execConfig.HostLimits.FileSizeMB
		hostNProc = execConfig.HostLimits.NProc

		dockerImage = strings.TrimSpace(execConfig.Docker.Image)
		dockerNetwork = strings.TrimSpace(execConfig.Docker.Network)
		if dockerNetwork == "" {
			dockerNetwork = "none"
		}
		dockerReadOnly = execConfig.Docker.ReadOnlyRootFS
		dockerMemoryMB = execConfig.Docker.MemoryMB
		dockerCPUs = execConfig.Docker.CPUs
		dockerPidsLimit = execConfig.Docker.PidsLimit
	} else {
		denyPatterns = append(denyPatterns, defaultDenyPatterns...)
	}

	return &ExecTool{
		workingDir:           workingDir,
		timeout:              60 * time.Second,
		denyPatterns:         denyPatterns,
		allowPatterns:        nil,
		customAllowPatterns:  customAllowPatterns,
		restrictToWorkspace:  restrict,
		processes:            NewProcessManager(defaultProcessMaxOutputChars),
		backend:              backend,
		envMode:              envMode,
		envAllow:             envAllow,
		hostMemoryMB:         hostMemoryMB,
		hostCPUSeconds:       hostCPUSeconds,
		hostFileSizeMB:       hostFileSizeMB,
		hostNProc:            hostNProc,
		dockerImage:          dockerImage,
		dockerNetwork:        dockerNetwork,
		dockerReadOnlyRootFS: dockerReadOnly,
		dockerMemoryMB:       dockerMemoryMB,
		dockerCPUs:           dockerCPUs,
		dockerPidsLimit:      dockerPidsLimit,
	}, nil
}

func (t *ExecTool) Name() string {
	return "exec"
}

func (t *ExecTool) Description() string {
	return "Execute a shell command in the workspace directory and return stdout/stderr. " +
		"Input: command (string, required). " +
		"Output: stdout content, with stderr appended if present. Includes exit code on failure. " +
		"Constraints: Dangerous commands (rm -rf, sudo, etc.) are blocked. " +
		"Commands are restricted to the workspace directory. " +
		"Default timeout: 60 seconds (override with timeout_seconds). " +
		"Use background=true for long-running commands. " +
		"When NOT to use: for reading file content (use read_file instead), for writing files (use write_file/edit_file)."
}

func (t *ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "Optional working directory for the command",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Start command in background and manage it via process tool",
			},
			"yield_ms": map[string]any{
				"type":        "integer",
				"description": "Wait this many milliseconds before returning running status",
				"minimum":     0.0,
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Override command timeout in seconds (0 disables timeout)",
				"minimum":     0.0,
			},
		},
		"required": []string{"command"},
	}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	command, ok := args["command"].(string)
	if !ok {
		return ErrorResult("command is required")
	}

	cwd := t.workingDir
	if wd, ok := args["working_dir"].(string); ok && wd != "" {
		if t.restrictToWorkspace && t.workingDir != "" {
			resolvedWD, err := validatePath(wd, t.workingDir, true)
			if err != nil {
				return ErrorResult("Command blocked by safety guard (" + err.Error() + ")")
			}
			cwd = resolvedWD
		} else {
			cwd = wd
		}
	}

	if cwd == "" {
		wd, err := os.Getwd()
		if err == nil {
			cwd = wd
		}
	}

	if guardError := t.guardCommand(command, cwd); guardError != "" {
		return ErrorResult(guardError)
	}

	background, err := parseBoolArg(args, "background", false)
	if err != nil {
		return ErrorResult(err.Error())
	}

	yieldMS, err := parseOptionalIntArg(args, "yield_ms", 0, 0, 60*60*1000)
	if err != nil {
		return ErrorResult(err.Error())
	}

	timeoutSeconds, hasTimeoutOverride, err := readOptionalIntArg(args, "timeout_seconds", 0, 24*60*60)
	if err != nil {
		return ErrorResult(err.Error())
	}

	timeout := t.timeout
	if hasTimeoutOverride {
		if timeoutSeconds == 0 {
			timeout = 0
		} else {
			timeout = time.Duration(timeoutSeconds) * time.Second
		}
	}

	if !background && yieldMS <= 0 {
		if strings.EqualFold(strings.TrimSpace(t.backend), "docker") {
			if !t.restrictToWorkspace {
				return ErrorResult("docker sandbox requires restrict_to_workspace=true")
			}
			return t.executeDockerSync(ctx, command, cwd, timeout)
		}
		return t.executeSync(ctx, command, cwd, timeout)
	}

	if strings.EqualFold(strings.TrimSpace(t.backend), "docker") {
		return ErrorResult("docker exec backend does not support background/yield mode (set tools.exec.backend=host)")
	}

	return t.executeManaged(
		ctx,
		command,
		cwd,
		background,
		time.Duration(yieldMS)*time.Millisecond,
		timeout,
	)
}

func (t *ExecTool) executeDockerSync(ctx context.Context, command, cwd string, timeout time.Duration) *ToolResult {
	image := strings.TrimSpace(t.dockerImage)
	if image == "" {
		return ErrorResult("docker backend selected but tools.exec.docker.image is empty")
	}
	workspace := strings.TrimSpace(t.workingDir)
	if workspace == "" {
		return ErrorResult("docker backend requires a non-empty workingDir")
	}

	// timeout == 0 means no timeout.
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	containerWD := "/workspace"
	if strings.TrimSpace(cwd) != "" && strings.TrimSpace(workspace) != "" {
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

	args := []string{
		"run",
		"--rm",
		"--init",
		"--network", network,
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", containerWD,
	}
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
		args = append(args,
			"--read-only",
			"--tmpfs", "/tmp:rw,size=64m",
			"--tmpfs", "/var/tmp:rw,size=64m",
		)
	}
	// Run the command via a shell for compatibility with existing behavior.
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
	go func() {
		done <- cmd.Wait()
	}()

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
			return &ToolResult{
				ForLLM:  msg,
				ForUser: msg,
				IsError: true,
			}
		}
		output += fmt.Sprintf("\nExit code: %v", err)
	}

	output = truncateExecOutput(output)

	if err != nil {
		return &ToolResult{
			ForLLM:  output,
			ForUser: output,
			IsError: true,
		}
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: false,
	}
}

func (t *ExecTool) executeSync(ctx context.Context, command, cwd string, timeout time.Duration) *ToolResult {
	// timeout == 0 means no timeout.
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := shellCommand(cmdCtx, t.withHostLimits(command))
	if cwd != "" {
		cmd.Dir = cwd
	}
	t.applyEnvPolicy(cmd)

	prepareCommandForTermination(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

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
			return &ToolResult{
				ForLLM:  msg,
				ForUser: msg,
				IsError: true,
			}
		}
		output += fmt.Sprintf("\nExit code: %v", err)
	}

	output = truncateExecOutput(output)

	if err != nil {
		return &ToolResult{
			ForLLM:  output,
			ForUser: output,
			IsError: true,
		}
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: false,
	}
}

func (t *ExecTool) executeManaged(
	ctx context.Context,
	command, cwd string,
	background bool,
	yield time.Duration,
	timeout time.Duration,
) *ToolResult {
	if t.processes == nil {
		return t.executeSync(ctx, command, cwd, timeout)
	}

	baseCtx := context.Background()
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(baseCtx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(baseCtx)
	}

	cmd := shellCommand(cmdCtx, t.withHostLimits(command))
	if cwd != "" {
		cmd.Dir = cwd
	}
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
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	sessionID := t.processes.StartSession(command, cwd, cmd, stdinPipe, cancel)
	done := make(chan struct{})
	go t.watchManagedCommand(sessionID, cmdCtx, cmd, stdoutPipe, stderrPipe, done)

	if background {
		return t.runningSessionResult(sessionID)
	}

	if yield <= 0 {
		yield = 10 * time.Second
	}
	timer := time.NewTimer(yield)
	defer timer.Stop()

	select {
	case <-done:
		pollResult, err := t.processes.Poll(sessionID, 0)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to read managed command result: %v", err))
		}
		return formatManagedCompletion(pollResult, timeout)
	case <-timer.C:
		return t.runningSessionResult(sessionID)
	case <-ctx.Done():
		_, _ = t.processes.Kill(sessionID)
		return ErrorResult(fmt.Sprintf("command canceled: %v", ctx.Err()))
	}
}

func (t *ExecTool) watchManagedCommand(
	sessionID string,
	cmdCtx context.Context,
	cmd *exec.Cmd,
	stdoutPipe, stderrPipe io.ReadCloser,
	done chan<- struct{},
) {
	defer close(done)

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

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-cmdCtx.Done():
		if termErr := terminateProcessTree(cmd); termErr != nil {
			logger.DebugCF("tools/shell", "terminate process tree failed", map[string]any{"error": termErr.Error()})
		}
		select {
		case waitErr = <-waitDone:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			waitErr = <-waitDone
		}
	}

	<-stdoutDone
	<-stderrDone

	t.processes.MarkExited(sessionID, waitErr, errors.Is(cmdCtx.Err(), context.DeadlineExceeded))
}

func (t *ExecTool) streamManagedOutput(sessionID string, reader io.ReadCloser, stderr bool) {
	defer reader.Close()

	buf := make([]byte, 4096)
	wroteStderrHeader := false
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if stderr && !wroteStderrHeader {
				t.processes.AppendOutput(sessionID, "\nSTDERR:\n")
				wroteStderrHeader = true
			}
			t.processes.AppendOutput(sessionID, string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (t *ExecTool) runningSessionResult(sessionID string) *ToolResult {
	payload := map[string]any{
		"status":     "running",
		"session_id": sessionID,
	}
	if snap, ok := t.processes.GetSnapshot(sessionID); ok {
		payload["pid"] = snap.PID
		payload["started_at"] = snap.StartedAt
		payload["command"] = snap.Command
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to encode background exec result: %v", err))
	}
	return SilentResult(string(data))
}
