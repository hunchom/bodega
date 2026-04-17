package ui

import (
	"strings"

	"github.com/hunchom/bodega/internal/ui/theme"
)

type Align int

const (
	AlignLeft Align = iota
	AlignRight
)

type Table struct {
	Headers []string
	Aligns  []Align
	Rows    [][]string
}

func (t Table) Render() string {
	cols := len(t.Headers)
	if cols == 0 {
		return ""
	}
	widths := make([]int, cols)
	for i, h := range t.Headers {
		widths[i] = len(h)
	}
	for _, r := range t.Rows {
		for i := 0; i < cols && i < len(r); i++ {
			if n := visualLen(r[i]); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	write := func(vals []string, head bool) {
		for i, v := range vals {
			if i >= cols {
				break
			}
			pad := widths[i] - visualLen(v)
			if pad < 0 {
				pad = 0
			}
			if i < len(t.Aligns) && t.Aligns[i] == AlignRight {
				b.WriteString(strings.Repeat(" ", pad))
				if head {
					b.WriteString(theme.Header.Render(v))
				} else {
					b.WriteString(v)
				}
			} else {
				if head {
					b.WriteString(theme.Header.Render(v))
				} else {
					b.WriteString(v)
				}
				b.WriteString(strings.Repeat(" ", pad))
			}
			if i < cols-1 {
				b.WriteString("  ")
			}
		}
		b.WriteByte('\n')
	}
	write(t.Headers, true)
	for _, r := range t.Rows {
		write(r, false)
	}
	return b.String()
}

// visualLen counts runes; good enough for ASCII / BMP. Ignores ANSI since tests set NoColor.
func visualLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
