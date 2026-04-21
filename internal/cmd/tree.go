package cmd

import (
	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func toUI(n *backend.DepTree) *ui.TreeNode {
	if n == nil {
		return &ui.TreeNode{}
	}
	node := &ui.TreeNode{Label: n.Name}
	for _, c := range n.Children {
		node.Children = append(node.Children, toUI(c))
	}
	return node
}

func newTreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "tree <pkg>",
		Aliases: []string{"deplist"},
		Short:   "Forward dependency tree",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			t, err := app.Registry.Primary().Deps(app.Ctx, args[0])
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(t)
			}
			if t == nil || (t.Name == "" && len(t.Children) == 0) {
				app.W.Println(theme.Muted.Render("no dependencies"))
				return nil
			}
			app.W.Printf("%s", ui.RenderTree(toUI(t)))
			return nil
		},
	}
}

func newWhyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "why <pkg>",
		Short: "Show reverse dependencies (what pulled this in)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			rdeps, err := app.Registry.Primary().ReverseDeps(app.Ctx, args[0])
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(rdeps)
			}
			if len(rdeps) == 0 {
				app.W.Println(theme.Muted.Render("nothing depends on " + args[0]))
				return nil
			}
			root := &ui.TreeNode{Label: args[0]}
			for _, n := range rdeps {
				root.Children = append(root.Children, &ui.TreeNode{Label: n})
			}
			app.W.Printf("%s", ui.RenderTree(root))
			return nil
		},
	}
}
