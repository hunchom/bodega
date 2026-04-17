package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/hunchom/bodega/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(c *cobra.Command, _ []string) error {
			if Flags.JSON {
				b, err := json.Marshal(map[string]string{
					"version": version.Version,
					"commit":  version.Commit,
					"date":    version.Date,
				})
				if err != nil {
					return err
				}
				fmt.Fprintln(c.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintf(c.OutOrStdout(), "yum %s (%s, %s)\n", version.Version, version.Commit, version.Date)
			return nil
		},
	}
}
