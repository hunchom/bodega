package ui

import (
	"strings"
	"testing"

	"github.com/hunchom/yum/internal/ui/theme"
)

func TestTree(t *testing.T) {
	theme.NoColor = true
	theme.Load()
	defer func() { theme.NoColor = false; theme.Load() }()

	root := &TreeNode{Label: "root", Children: []*TreeNode{
		{Label: "a", Children: []*TreeNode{{Label: "a1"}, {Label: "a2"}}},
		{Label: "b"},
	}}
	got := RenderTree(root)
	want := strings.Join([]string{
		"root",
		"├─ a",
		"│  ├─ a1",
		"│  └─ a2",
		"└─ b",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
