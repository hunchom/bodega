package cmd

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

// isWhichFormulaNotFound detects brew's empty-result exit-1 pattern for
// `brew which-formula <cmd>`. brewErr formats that as "brew which-formula
// <cmd>: exit 1" when brew prints nothing to stdout or stderr. Matching on
// the shape of the message is ugly but keeps the no-match handling in the
// cmd layer (brew.go is owned elsewhere).
func isWhichFormulaNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "which-formula") && strings.HasSuffix(msg, ": exit 1")
}

// visualPlainLen returns the rune count of s with ANSI SGR escapes removed,
// so dividers line up under styled headers. Good enough for the subset of
// escapes lipgloss emits (CSI ... m).
func visualPlainLen(s string) int {
	n := 0
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if size < 1 {
			size = 1
		}
		n++
		i += size
	}
	return n
}

func newProvidesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "provides <cmd>",
		Aliases: []string{"whatprovides"},
		Short:   "Find which formula installs a command",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			names, err := app.Registry.Primary().Provides(app.Ctx, args[0])
			// `brew which-formula` exits 1 when nothing matches, with empty
			// stdout/stderr. Treat that as a successful "no results"
			// instead of an error so the user sees the friendly message
			// and the shell exit code is 0.
			if err != nil && isWhichFormulaNotFound(err) {
				names, err = nil, nil
			}
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(names)
			}
			if len(names) == 0 {
				app.W.Errorf("%s\n", theme.Muted.Render(fmt.Sprintf("no formula provides '%s'", args[0])))
				return nil
			}
			// Enrich each name with tap+desc from Info(). Info hits the disk
			// cache when warm; a miss is tolerable — we just leave the row
			// blank rather than bailing out the whole command.
			rows := make([][]string, 0, len(names))
			for _, n := range names {
				tap, desc := "", ""
				if p, err := app.Registry.Primary().Info(app.Ctx, n); err == nil && p != nil {
					tap = p.Tap
					desc = p.Desc
				}
				rows = append(rows, []string{n, tap, desc})
			}
			tbl := ui.Table{
				Headers: []string{"name", "tap", "desc"},
				Aligns:  []ui.Align{ui.AlignLeft, ui.AlignLeft, ui.AlignLeft},
				Rows:    rows,
			}
			// Render the table with a thin divider between header and rows
			// and a one-space left gutter on every line, matching repolist.
			lines := strings.Split(strings.TrimRight(tbl.Render(), "\n"), "\n")
			if len(lines) == 0 {
				return nil
			}
			// Divider width = visual length of the widest rendered line.
			// Header line has ANSI escapes when colours are on, so count
			// the stripped version.
			width := 0
			for _, l := range lines {
				if n := visualPlainLen(l); n > width {
					width = n
				}
			}
			app.W.Printf(" %s\n", lines[0])
			app.W.Printf(" %s\n", theme.Muted.Render(strings.Repeat("─", width)))
			for _, l := range lines[1:] {
				app.W.Printf(" %s\n", l)
			}
			return nil
		},
	}
}

func newRepolistCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repolist",
		Short: "List active taps",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			taps, err := app.Registry.Primary().Taps(app.Ctx)
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(taps)
			}

			// Width of the divider: the widest of the header word "tap" or
			// any tap name, clamped to at least the sample width used in the
			// spec (30) so single-tap machines don't get a stubby line.
			w := len("tap")
			for _, t := range taps {
				if n := len(t); n > w {
					w = n
				}
			}
			if w < 30 {
				w = 30
			}
			app.W.Printf(" %s\n", theme.Header.Render("tap"))
			app.W.Printf(" %s\n", theme.Muted.Render(strings.Repeat("─", w)))
			for _, t := range taps {
				app.W.Printf(" %s\n", t)
			}
			app.W.Println()
			noun := "taps"
			if len(taps) == 1 {
				noun = "tap"
			}
			app.W.Printf(" %s\n", theme.Muted.Render(fmt.Sprintf("%d %s", len(taps), noun)))
			return nil
		},
	}
}

func newCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean [all]",
		Short: "Remove old versions and caches",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			deep := len(args) == 1 && args[0] == "all"
			return app.Registry.Primary().Cleanup(app.Ctx, deep)
		},
	}
}

func newPinCmd(pin bool) *cobra.Command {
	use := "pin"
	if !pin {
		use = "unpin"
	}
	return &cobra.Command{
		Use:   use + " <pkg>",
		Short: fmt.Sprintf("%s a package version", use),
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			return app.Registry.Primary().Pin(app.Ctx, args[0], pin)
		},
	}
}
