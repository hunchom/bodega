package ui

import "strings"

type TreeNode struct {
	Label    string
	Children []*TreeNode
}

func RenderTree(n *TreeNode) string {
	var b strings.Builder
	b.WriteString(n.Label + "\n")
	renderChildren(&b, n.Children, "")
	return b.String()
}

func renderChildren(b *strings.Builder, ch []*TreeNode, prefix string) {
	for i, c := range ch {
		last := i == len(ch)-1
		var branch, extend string
		if last {
			branch = "└─ "
			extend = "   "
		} else {
			branch = "├─ "
			extend = "│  "
		}
		b.WriteString(prefix + branch + c.Label + "\n")
		renderChildren(b, c.Children, prefix+extend)
	}
}
