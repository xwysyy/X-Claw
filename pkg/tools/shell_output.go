package tools

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func formatManagedCompletion(result ProcessPollResult, timeout time.Duration) *ToolResult {
	output := truncateExecOutput(result.Output)
	status := strings.ToLower(result.Session.Status)
	if status == "" {
		status = "completed"
	}

	switch status {
	case "completed":
		return UserResult(output)
	case "timeout":
		msg := fmt.Sprintf("Command timed out after %v", timeout)
		if output != "(no output)" {
			msg = output + "\n" + msg
		}
		return ErrorResult(msg)
	default:
		if result.Session.ExitError != "" && !strings.Contains(output, result.Session.ExitError) {
			output += "\nExit code: " + result.Session.ExitError
		}
		return &ToolResult{
			ForLLM:  output,
			ForUser: output,
			IsError: true,
		}
	}
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func (t *ExecTool) withHostLimits(command string) string {
	if t == nil {
		return command
	}
	if runtime.GOOS == "windows" {
		return command
	}
	if !strings.EqualFold(strings.TrimSpace(t.backend), "host") {
		return command
	}

	memMB := t.hostMemoryMB
	cpuSeconds := t.hostCPUSeconds
	fileSizeMB := t.hostFileSizeMB
	nproc := t.hostNProc

	parts := make([]string, 0, 4)
	if memMB > 0 {
		memKB := memMB * 1024
		parts = append(parts, fmt.Sprintf(
			"ulimit -v %d || { echo 'exec host_limits: failed to set ulimit -v' 1>&2; exit 1; }",
			memKB,
		))
	}
	if cpuSeconds > 0 {
		parts = append(parts, fmt.Sprintf(
			"ulimit -t %d || { echo 'exec host_limits: failed to set ulimit -t' 1>&2; exit 1; }",
			cpuSeconds,
		))
	}
	if fileSizeMB > 0 {
		// ulimit -f uses 512-byte blocks on most shells.
		blocks := fileSizeMB * 2048
		parts = append(parts, fmt.Sprintf(
			"ulimit -f %d || { echo 'exec host_limits: failed to set ulimit -f' 1>&2; exit 1; }",
			blocks,
		))
	}
	if nproc > 0 {
		parts = append(parts, fmt.Sprintf(
			"ulimit -u %d || { echo 'exec host_limits: failed to set ulimit -u' 1>&2; exit 1; }",
			nproc,
		))
	}

	if len(parts) == 0 {
		return command
	}

	return strings.Join(parts, " && ") + " && " + command
}

func truncateExecOutput(output string) string {
	if output == "" {
		output = "(no output)"
	}

	const maxLen = 10000
	if len(output) > maxLen {
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, %d more chars)", len(output)-maxLen)
	}
	return output
}

func readOptionalIntArg(args map[string]any, key string, minVal, maxVal int) (int, bool, error) {
	raw, exists := args[key]
	if !exists {
		return 0, false, nil
	}

	n, err := toInt(raw)
	if err != nil {
		return 0, true, fmt.Errorf("%s must be an integer", key)
	}
	if n < minVal || n > maxVal {
		return 0, true, fmt.Errorf("%s must be between %d and %d", key, minVal, maxVal)
	}
	return n, true, nil
}
