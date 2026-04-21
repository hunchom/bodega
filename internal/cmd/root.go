package cmd

import (
	"github.com/spf13/cobra"
)

type GlobalFlags struct {
	JSON      bool
	Yes       bool
	NoColor   bool
	Debug     bool
	DryRun    bool
	Config    string
	Refresh   bool // --refresh: force tap refresh before this command
	NoRefresh bool // --no-refresh: skip tap refresh even if stale
}

var Flags = &GlobalFlags{}

type ExitErr struct {
	Short string
	Code  int
}

func (e *ExitErr) Error() string { return e.Short }

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "yum",
		Short:         "Modern package manager for macOS",
		Long:          "yum — yum/dnf command surface over Homebrew, with rollback, history, and a proper TUI.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.PersistentFlags().BoolVar(&Flags.JSON, "json", false, "machine-readable output")
	root.PersistentFlags().BoolVarP(&Flags.Yes, "yes", "y", false, "assume yes on prompts")
	root.PersistentFlags().BoolVar(&Flags.NoColor, "no-color", false, "disable ANSI colors")
	root.PersistentFlags().BoolVar(&Flags.Debug, "debug", false, "verbose debug output")
	root.PersistentFlags().BoolVar(&Flags.DryRun, "dry-run", false, "show what would happen")
	root.PersistentFlags().StringVar(&Flags.Config, "config", "", "override config path")
	root.PersistentFlags().BoolVar(&Flags.Refresh, "refresh", false, "force brew tap refresh before the command")
	root.PersistentFlags().BoolVar(&Flags.NoRefresh, "no-refresh", false, "skip brew tap refresh even when stale")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newInstallCmd())
	root.AddCommand(newRemoveCmd())
	root.AddCommand(newReinstallCmd())
	root.AddCommand(newUpgradeCmd())
	root.AddCommand(newAutoremoveCmd())
	root.AddCommand(newInfoCmd())
	root.AddCommand(newOutdatedCmd())
	root.AddCommand(newTreeCmd())
	root.AddCommand(newWhyCmd())
	root.AddCommand(newSizeCmd())
	root.AddCommand(newProvidesCmd())
	root.AddCommand(newRepolistCmd())
	root.AddCommand(newCleanCmd())
	root.AddCommand(newDuplicatesCmd())
	root.AddCommand(newPinCmd(true))
	root.AddCommand(newPinCmd(false))
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newLogCmd())
	root.AddCommand(newRollbackCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newManifestCmd())
	root.AddCommand(newCompletionsCmd(root))
	return root
}
