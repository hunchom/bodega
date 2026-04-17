package cmd

import (
	"fmt"

	"github.com/hunchom/yum/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(c *cobra.Command, _ []string) error {
			fmt.Fprintf(c.OutOrStdout(), "yum %s (%s, %s)\n", version.Version, version.Commit, version.Date)
			return nil
		},
	}
}
