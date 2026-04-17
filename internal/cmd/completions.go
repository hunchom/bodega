package cmd

import "github.com/spf13/cobra"

func newCompletionsCmd(root *cobra.Command) *cobra.Command {
	c := &cobra.Command{
		Use:                   "completions [zsh|bash|fish|powershell]",
		Short:                 "Generate shell completion script",
		ValidArgs:             []string{"zsh", "bash", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		DisableFlagsInUseLine: true,
		RunE: func(c *cobra.Command, args []string) error {
			switch args[0] {
			case "zsh":
				return root.GenZshCompletion(c.OutOrStdout())
			case "bash":
				return root.GenBashCompletion(c.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(c.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(c.OutOrStdout())
			}
			return nil
		},
	}
	return c
}
