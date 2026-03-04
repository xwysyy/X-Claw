package doctor

import "github.com/spf13/cobra"

type doctorOptions struct {
	Path string
	JSON bool
}

func NewDoctorCommand() *cobra.Command {
	var opts doctorOptions

	cmd := &cobra.Command{
		Use:           "doctor",
		Short:         "Run diagnostics and report common issues",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return doctorCmd(opts)
		},
	}

	cmd.Flags().StringVar(&opts.Path, "config", "", "Config file path (default: $PICOCLAW_CONFIG or ~/.picoclaw/config.json)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output report as JSON (for scripts)")

	return cmd
}
