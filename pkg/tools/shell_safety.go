package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	defaultDenyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		// Match disk wiping commands (must be followed by space/args).
		regexp.MustCompile(`\b(format|mkfs|diskpart)\b\s`),
		regexp.MustCompile(`\bdd\s+if=`),
		// Block writes to block devices (all common naming schemes).
		regexp.MustCompile(
			`>\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme\d|mmcblk\d|loop\d|dm-\d|md\d|sr\d|nbd\d)`,
		),
		regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
		regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),
		regexp.MustCompile(`\$\([^)]+\)`),
		regexp.MustCompile(`\$\{[^}]+\}`),
		regexp.MustCompile("`[^`]+`"),
		regexp.MustCompile(`\|\s*sh\b`),
		regexp.MustCompile(`\|\s*bash\b`),
		regexp.MustCompile(`;\s*rm\s+-[rf]`),
		regexp.MustCompile(`&&\s*rm\s+-[rf]`),
		regexp.MustCompile(`\|\|\s*rm\s+-[rf]`),
		regexp.MustCompile(`<<\s*EOF`),
		regexp.MustCompile(`\$\(\s*cat\s+`),
		regexp.MustCompile(`\$\(\s*curl\s+`),
		regexp.MustCompile(`\$\(\s*wget\s+`),
		regexp.MustCompile(`\$\(\s*which\s+`),
		regexp.MustCompile(`\bsudo\b`),
		regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\b`),
		regexp.MustCompile(`\bchown\b`),
		regexp.MustCompile(`\bpkill\b`),
		regexp.MustCompile(`\bkillall\b`),
		regexp.MustCompile(`\bkill\s+-[9]\b`),
		regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bwget\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bnpm\s+install\s+-g\b`),
		regexp.MustCompile(`\bpip\s+install\s+--user\b`),
		regexp.MustCompile(`\bapt\s+(install|remove|purge)\b`),
		regexp.MustCompile(`\byum\s+(install|remove)\b`),
		regexp.MustCompile(`\bdnf\s+(install|remove)\b`),
		regexp.MustCompile(`\bdocker\s+run\b`),
		regexp.MustCompile(`\bdocker\s+exec\b`),
		regexp.MustCompile(`\bgit\s+push\b`),
		regexp.MustCompile(`\bgit\s+force\b`),
		regexp.MustCompile(`\bssh\b.*@`),
		regexp.MustCompile(`\beval\b`),
		regexp.MustCompile(`\bsource\s+.*\.sh\b`),
	}

	// absolutePathPattern matches absolute file paths in commands (Unix and Windows).
	absolutePathPattern = regexp.MustCompile(`[A-Za-z]:\\[^\\\"']+|/[^\s\"']+`)

	envVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

	// safePaths are kernel pseudo-devices that are always safe to reference in
	// commands, regardless of workspace restriction. They contain no user data
	// and cannot cause destructive writes.
	safePaths = map[string]bool{
		"/dev/null":    true,
		"/dev/zero":    true,
		"/dev/random":  true,
		"/dev/urandom": true,
		"/dev/stdin":   true,
		"/dev/stdout":  true,
		"/dev/stderr":  true,
	}
)

var platformExecEnvAllow = []string{
	"PATH",
	"HOME",
	"USER",
	"LOGNAME",
	"SHELL",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"TERM",
	"TZ",
	"TMPDIR",
	// Proxy support.
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"NO_PROXY",
	"http_proxy",
	"https_proxy",
	"no_proxy",
}

func buildExecEnvAllowMap(configAllow []string) map[string]bool {
	allow := make(map[string]bool, len(platformExecEnvAllow)+len(configAllow))
	for _, name := range platformExecEnvAllow {
		name = strings.TrimSpace(name)
		if name != "" {
			allow[name] = true
		}
	}
	for _, name := range configAllow {
		name = strings.TrimSpace(name)
		if envVarNamePattern.MatchString(name) {
			allow[name] = true
		}
	}
	return allow
}

func filterExecEnv(env []string, allow map[string]bool) []string {
	if len(env) == 0 || len(allow) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		name := kv[:eq]
		if allow[name] {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}

func (t *ExecTool) applyEnvPolicy(cmd *exec.Cmd) {
	if t == nil || cmd == nil {
		return
	}
	// Only apply env filtering for host backend. For docker backend, the executed
	// user command runs inside a container and we do not pass env; filtering the
	// docker CLI environment can break rootless/remote docker setups.
	if strings.EqualFold(strings.TrimSpace(t.backend), "docker") {
		return
	}
	if strings.EqualFold(strings.TrimSpace(t.envMode), "inherit") {
		// Leave cmd.Env nil → inherit everything.
		return
	}
	cmd.Env = filterExecEnv(os.Environ(), t.envAllow)
}

func (t *ExecTool) guardCommand(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	// Custom allow patterns exempt a command from deny checks.
	explicitlyAllowed := false
	for _, pattern := range t.customAllowPatterns {
		if pattern.MatchString(lower) {
			explicitlyAllowed = true
			break
		}
	}

	if !explicitlyAllowed {
		for _, pattern := range t.denyPatterns {
			if pattern.MatchString(lower) {
				return "Command blocked by safety guard (dangerous pattern detected)"
			}
		}
	}

	if len(t.allowPatterns) > 0 {
		allowed := false
		for _, pattern := range t.allowPatterns {
			if pattern.MatchString(lower) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "Command blocked by safety guard (not in allowlist)"
		}
	}

	if t.restrictToWorkspace {
		if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
			return "Command blocked by safety guard (path traversal detected). " +
				"Suggestion: Use absolute paths within the workspace directory, " +
				"or use read_file/list_dir tools to access files."
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return ""
		}

		matches := absolutePathPattern.FindAllStringIndex(cmd, -1)
		for _, match := range matches {
			raw := cmd[match[0]:match[1]]
			if !pathMatchHasTokenBoundary(cmd, match[0]) {
				// absolutePathPattern is intentionally broad and can match substrings
				// inside relative paths (e.g. "skills/foo/bar" contains "/foo/bar").
				// Only treat this match as an absolute path when it begins at a token
				// boundary, otherwise skip it.
				continue
			}
			if pathMatchIsEnvAssignmentValue(cmd, match[0]) || pathMatchIsURLSegment(cmd, raw, match[0]) {
				continue
			}

			p, err := filepath.Abs(raw)
			if err != nil {
				continue
			}

			if safePaths[p] {
				continue
			}

			rel, err := filepath.Rel(cwdPath, p)
			if err != nil {
				continue
			}

			if strings.HasPrefix(rel, "..") {
				return "Command blocked by safety guard (path outside working dir). " +
					"Suggestion: Only access files within the workspace directory. " +
					"Use list_dir to see available files."
			}
		}
	}

	return ""
}

func pathMatchHasTokenBoundary(command string, matchStart int) bool {
	if matchStart <= 0 {
		return true
	}
	if matchStart > len(command) {
		return false
	}

	prev := command[matchStart-1]
	// Whitespace and common shell metacharacters delimit tokens.
	if prev <= ' ' {
		return true
	}
	switch prev {
	case '"', '\'', '(', ')', '[', ']', '{', '}', '<', '>', '|', '&', ';', '=':
		return true
	default:
		return false
	}
}

func pathMatchIsEnvAssignmentValue(command string, matchStart int) bool {
	if matchStart <= 0 || matchStart > len(command) {
		return false
	}

	tokenStart := strings.LastIndexAny(command[:matchStart], " \t\r\n") + 1
	if tokenStart < 0 || tokenStart >= matchStart {
		return false
	}

	prefix := command[tokenStart:matchStart]
	eq := strings.Index(prefix, "=")
	if eq <= 0 {
		return false
	}

	return envVarNamePattern.MatchString(prefix[:eq])
}

// pathMatchIsURLSegment detects path-like regex matches that are actually
// URL path segments (e.g. "/owner/repo.git" in "https://github.com/owner/repo.git").
func pathMatchIsURLSegment(command, raw string, matchStart int) bool {
	if matchStart <= 0 || matchStart > len(command) {
		return false
	}

	tokenStart := strings.LastIndexAny(command[:matchStart], " \t\r\n") + 1
	if tokenStart < 0 || tokenStart >= matchStart {
		return false
	}

	prefix := command[tokenStart:matchStart]
	if strings.Contains(prefix, "://") {
		return true
	}

	return strings.HasPrefix(raw, "//") && strings.HasSuffix(prefix, ":")
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

func (t *ExecTool) ProcessManager() *ProcessManager {
	return t.processes
}

func (t *ExecTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ExecTool) SetAllowPatterns(patterns []string) error {
	t.allowPatterns = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("invalid allow pattern %q: %w", p, err)
		}
		t.allowPatterns = append(t.allowPatterns, re)
	}
	return nil
}
