package app

import "github.com/spf13/cobra"

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = cmd.OutOrStdout().Write([]byte(versionString() + "\n"))
		},
	}
}

