package tools

import (
	"context"
	"fmt"
	"os"
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
