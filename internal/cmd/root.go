package cmd

import (
	"github.com/spf13/cobra"
)

type GlobalFlags struct {
	JSON    bool
	Yes     bool
	NoColor bool
	Debug   bool
	DryRun  bool
	Config  string
}

var Flags = &GlobalFlags{}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "yum",
		Short:         "Modern package manager for macOS",
		Long:          "yum — yum/dnf command surface over Homebrew, with rollback, history, and a proper TUI.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&Flags.JSON, "json", false, "machine-readable output")
	root.PersistentFlags().BoolVarP(&Flags.Yes, "yes", "y", false, "assume yes on prompts")
	root.PersistentFlags().BoolVar(&Flags.NoColor, "no-color", false, "disable ANSI colors")
	root.PersistentFlags().BoolVar(&Flags.Debug, "debug", false, "verbose debug output")
	root.PersistentFlags().BoolVar(&Flags.DryRun, "dry-run", false, "show what would happen")
	root.PersistentFlags().StringVar(&Flags.Config, "config", "", "override config path")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newInstallCmd())
	root.AddCommand(newRemoveCmd())
	root.AddCommand(newReinstallCmd())
	root.AddCommand(newUpgradeCmd())
	root.AddCommand(newAutoremoveCmd())
	root.AddCommand(newInfoCmd())
	return root
}
