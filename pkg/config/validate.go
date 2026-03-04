package config

import (
	"fmt"
	"strings"
)

// ValidationProblem describes a single config validation issue with an associated JSON path.
// The Path uses dot notation with array indices (e.g. "model_list[0].model_name").
type ValidationProblem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ConfigValidationError is returned by LoadConfig when validation fails.
// It is structured so CLI tools can render errors as JSON.
type ConfigValidationError struct {
	Problems []ValidationProblem
}

func (e *ConfigValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "config validation failed"
	}
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		path := strings.TrimSpace(p.Path)
		msg := strings.TrimSpace(p.Message)
		if msg == "" {
			continue
		}
		if path == "" {
			parts = append(parts, msg)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", path, msg))
	}
	if len(parts) == 0 {
		return "config validation failed"
	}
	return "config validation failed: " + strings.Join(parts, "; ")
}

// ValidateAll validates the full config and returns a structured error on failure.
func (c *Config) ValidateAll() error {
	if c == nil {
		return nil
	}
	problems := c.ValidationProblems()
	if len(problems) == 0 {
		return nil
	}
	return &ConfigValidationError{Problems: problems}
}

// ValidationProblems returns all validation problems discovered in this config.
func (c *Config) ValidationProblems() []ValidationProblem {
	if c == nil {
		return nil
	}

	var problems []ValidationProblem

	host := strings.TrimSpace(c.Gateway.Host)
	if host == "" {
		problems = append(problems, ValidationProblem{
			Path:    "gateway.host",
			Message: "host is required",
		})
	}

	if c.Gateway.Port <= 0 || c.Gateway.Port > 65535 {
		problems = append(problems, ValidationProblem{
			Path:    "gateway.port",
			Message: fmt.Sprintf("port must be in range 1..65535 (got %d)", c.Gateway.Port),
		})
	}

	problems = append(problems, c.modelListProblems()...)
	problems = append(problems, c.securityProblems()...)
	return problems
}

func (c *Config) modelListProblems() []ValidationProblem {
	if c == nil {
		return nil
	}

	var problems []ValidationProblem
	for i := range c.ModelList {
		pathPrefix := fmt.Sprintf("model_list[%d]", i)
		if strings.TrimSpace(c.ModelList[i].ModelName) == "" {
			problems = append(problems, ValidationProblem{
				Path:    pathPrefix + ".model_name",
				Message: "model_name is required",
			})
		}
		if strings.TrimSpace(c.ModelList[i].Model) == "" {
			problems = append(problems, ValidationProblem{
				Path:    pathPrefix + ".model",
				Message: "model is required",
			})
		}
	}
	return problems
}

func (c *Config) securityProblems() []ValidationProblem {
	if c == nil {
		return nil
	}

	var problems []ValidationProblem

	host := strings.TrimSpace(c.Gateway.Host)
	if host != "" && !isLoopbackHost(host) && !c.Security.BreakGlass.AllowPublicGateway {
		problems = append(problems, ValidationProblem{
			Path: "gateway.host",
			Message: fmt.Sprintf(
				"gateway.host=%q binds non-loopback; set security.break_glass.allow_public_gateway=true to acknowledge",
				host,
			),
		})
	}

	// Unsafe workspace settings must be explicitly acknowledged.
	if (!c.Agents.Defaults.RestrictToWorkspace || c.Agents.Defaults.AllowReadOutsideWorkspace) &&
		!c.Security.BreakGlass.AllowUnsafeWorkspace {
		details := make([]string, 0, 2)
		if !c.Agents.Defaults.RestrictToWorkspace {
			details = append(details, "restrict_to_workspace=false")
		}
		if c.Agents.Defaults.AllowReadOutsideWorkspace {
			details = append(details, "allow_read_outside_workspace=true")
		}
		suffix := ""
		if len(details) > 0 {
			suffix = " (" + strings.Join(details, ", ") + ")"
		}
		problems = append(problems, ValidationProblem{
			Path:    "agents.defaults",
			Message: "agents.defaults workspace restrictions are loosened" + suffix + "; set security.break_glass.allow_unsafe_workspace=true to acknowledge",
		})
	}

	// Disabling exec deny patterns is unsafe.
	if !c.Tools.Exec.EnableDenyPatterns && !c.Security.BreakGlass.AllowUnsafeExec {
		problems = append(problems, ValidationProblem{
			Path:    "tools.exec.enable_deny_patterns",
			Message: "tools.exec.enable_deny_patterns=false; set security.break_glass.allow_unsafe_exec=true to acknowledge",
		})
	}

	// Passing full env to host exec is unsafe.
	if strings.EqualFold(strings.TrimSpace(c.Tools.Exec.Env.Mode), "inherit") && !c.Security.BreakGlass.AllowExecInheritEnv {
		problems = append(problems, ValidationProblem{
			Path:    "tools.exec.env.mode",
			Message: "tools.exec.env.mode=\"inherit\"; set security.break_glass.allow_exec_inherit_env=true to acknowledge",
		})
	}

	// Docker sandbox networking must be acknowledged.
	if strings.EqualFold(strings.TrimSpace(c.Tools.Exec.Backend), "docker") {
		network := strings.ToLower(strings.TrimSpace(c.Tools.Exec.Docker.Network))
		if network != "" && network != "none" && !c.Security.BreakGlass.AllowDockerNetwork {
			problems = append(problems, ValidationProblem{
				Path: "tools.exec.docker.network",
				Message: fmt.Sprintf(
					"tools.exec.docker.network=%q is not \"none\"; set security.break_glass.allow_docker_network=true to acknowledge",
					strings.TrimSpace(c.Tools.Exec.Docker.Network),
				),
			})
		}
	}

	return problems
}

// ValidateConfigFile loads the config file and returns all validation problems.
// It never treats validation problems as a load error; callers can decide how to render/exit.
func ValidateConfigFile(path string) (*Config, []ValidationProblem, error) {
	cfg, err := loadConfigUnvalidated(path)
	if err != nil {
		return nil, nil, err
	}
	return cfg, cfg.ValidationProblems(), nil
}
