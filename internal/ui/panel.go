package ui

import (
	"strings"

	"github.com/hunchom/bodega/internal/ui/theme"
)

type Field struct {
	Key   string
	Value string
}

type Panel struct {
	Title  string
	Fields []Field
}

func (p Panel) Render() string {
	var b strings.Builder
	b.WriteString("┌ " + theme.Bold.Render(p.Title) + "\n")
	keyW := 0
	for _, f := range p.Fields {
		if len(f.Key) > keyW {
			keyW = len(f.Key)
		}
	}
	for _, f := range p.Fields {
		b.WriteString("│ ")
		b.WriteString(theme.Muted.Render(f.Key))
		b.WriteString(strings.Repeat(" ", keyW-len(f.Key)+2))
		b.WriteString(f.Value)
		b.WriteString("\n")
	}
	b.WriteString("└\n")
	return b.String()
}
