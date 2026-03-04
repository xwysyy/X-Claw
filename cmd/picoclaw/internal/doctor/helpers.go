package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/cmd/picoclaw/internal/cliutil"
	cfgpkg "github.com/sipeed/picoclaw/pkg/config"
)

type doctorSeverity string

const (
	severityInfo  doctorSeverity = "info"
	severityWarn  doctorSeverity = "warn"
	severityError doctorSeverity = "error"
)

type doctorCheck struct {
	ID       string         `json:"id"`
	OK       bool           `json:"ok"`
	Severity doctorSeverity `json:"severity"`

	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type doctorReport struct {
	Kind    string `json:"kind"`
	OK      bool   `json:"ok"`
	Version string `json:"version"`

	ConfigPath   string `json:"config_path,omitempty"`
	ConfigExists bool   `json:"config_exists,omitempty"`

	Workspace       string `json:"workspace,omitempty"`
	WorkspaceExists bool   `json:"workspace_exists,omitempty"`

	Checks []doctorCheck `json:"checks,omitempty"`
}

func doctorCmd(opts doctorOptions) error {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = internal.GetConfigPath()
	}
	path = filepath.Clean(path)

	report := doctorReport{
		Kind:         "picoclaw_doctor",
		Version:      internal.FormatVersion(),
		ConfigPath:   path,
		ConfigExists: cliutil.FileExists(path),
	}

	if !report.ConfigExists {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "config.exists",
			OK:       false,
			Severity: severityWarn,
			Message:  "config file not found; defaults will be used",
			Hint:     "run `picoclaw onboard` or set $PICOCLAW_CONFIG to a valid config.json",
		})
	} else {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "config.exists",
			OK:       true,
			Severity: severityInfo,
			Message:  "config file exists",
		})
	}

	cfg, problems, loadErr := cfgpkg.ValidateConfigFile(path)
	if loadErr != nil {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "config.load",
			OK:       false,
			Severity: severityError,
			Message:  "failed to load config",
			Hint:     loadErr.Error(),
		})
	} else {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "config.load",
			OK:       true,
			Severity: severityInfo,
			Message:  "config loaded",
		})
	}

	if len(problems) > 0 {
		for _, p := range problems {
			path := strings.TrimSpace(p.Path)
			msg := strings.TrimSpace(p.Message)
			if msg == "" {
				msg = "invalid"
			}
			report.Checks = append(report.Checks, doctorCheck{
				ID:       "config.validate",
				OK:       false,
				Severity: severityError,
				Path:     path,
				Message:  msg,
				Hint:     "run `picoclaw config validate` for a focused report",
			})
		}
	} else if loadErr == nil {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "config.validate",
			OK:       true,
			Severity: severityInfo,
			Message:  "config validation passed",
		})
	}

	workspace := ""
	if cfg != nil {
		workspace = strings.TrimSpace(cfg.WorkspacePath())
	}
	report.Workspace = workspace
	report.WorkspaceExists = workspace != "" && cliutil.DirExists(workspace)

	if workspace == "" {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "workspace.path",
			OK:       false,
			Severity: severityError,
			Message:  "workspace path is empty",
			Hint:     "set agents.defaults.workspace in config.json",
		})
	} else if report.WorkspaceExists {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "workspace.exists",
			OK:       true,
			Severity: severityInfo,
			Message:  "workspace directory exists",
		})
	} else {
		report.Checks = append(report.Checks, doctorCheck{
			ID:       "workspace.exists",
			OK:       false,
			Severity: severityWarn,
			Message:  "workspace directory does not exist yet",
			Hint:     "gateway/agent will create it on first run; if it fails, check permissions",
		})
	}

	// Compute overall OK: any severityError makes the report not OK.
	report.OK = true
	for _, c := range report.Checks {
		if c.Severity == severityError {
			report.OK = false
			break
		}
	}

	if opts.JSON {
		data, err := cliutil.MarshalIndentNoEscape(report)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	} else {
		printDoctorReport(report)
	}

	if report.OK {
		return nil
	}
	return fmt.Errorf("doctor found problems")
}

func printDoctorReport(report doctorReport) {
	fmt.Println("Doctor Report")
	fmt.Printf("Version: %s\n", report.Version)
	fmt.Printf("Config: %s (exists=%v)\n", report.ConfigPath, report.ConfigExists)
	if strings.TrimSpace(report.Workspace) != "" {
		fmt.Printf("Workspace: %s (exists=%v)\n", report.Workspace, report.WorkspaceExists)
	}
	fmt.Println()

	if len(report.Checks) == 0 {
		fmt.Println("No checks were run.")
		return
	}

	fmt.Println("Checks:")
	for _, c := range report.Checks {
		status := "OK"
		if !c.OK {
			status = strings.ToUpper(string(c.Severity))
		}
		line := fmt.Sprintf("  - [%s] %s", status, c.Message)
		if strings.TrimSpace(c.Path) != "" {
			line += fmt.Sprintf(" (%s)", strings.TrimSpace(c.Path))
		}
		fmt.Println(line)
		if strings.TrimSpace(c.Hint) != "" && (!c.OK || c.Severity != severityInfo) {
			fmt.Printf("    hint: %s\n", strings.TrimSpace(c.Hint))
		}
	}
}
