package ui

import (
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hunchom/yum/internal/ui/theme"
)

// Progress is a single-line bar. Call Update on byte counts; Done to finalize.
type Progress struct {
	Out     io.Writer
	Total   int64
	Prefix  string
	cur     atomic.Int64
	start   time.Time
	started atomic.Bool
	active  bool
}

const barWidth = 30

func (p *Progress) render() string {
	cur := p.cur.Load()
	var frac float64
	if p.Total > 0 {
		frac = float64(cur) / float64(p.Total)
		if frac > 1 {
			frac = 1
		}
	}
	filled := int(frac * barWidth)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	pct := int(frac * 100)
	return fmt.Sprintf("\r%s %s %s %3d%%",
		theme.Muted.Render(p.Prefix),
		theme.Accent.Render(bar),
		theme.Muted.Render(humanBytes(cur)+"/"+humanBytes(p.Total)),
		pct)
}

func (p *Progress) Start() {
	if p.started.Swap(true) {
		return
	}
	p.start = time.Now()
	p.active = true
	fmt.Fprint(p.Out, p.render())
}

func (p *Progress) Update(n int64) {
	if !p.active {
		return
	}
	p.cur.Store(n)
	fmt.Fprint(p.Out, p.render())
}

func (p *Progress) Done(ok bool) {
	if !p.active {
		return
	}
	p.active = false
	elapsed := time.Since(p.start).Round(time.Millisecond)
	mark := theme.OK.Render("✓")
	if !ok {
		mark = theme.Err.Render("✗")
	}
	fmt.Fprintf(p.Out, "\r%s %s %s\n", mark, p.Prefix, theme.Muted.Render(elapsed.String()))
}

func humanBytes(n int64) string {
	const k = 1024.0
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if f < k {
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= k
	}
	return fmt.Sprintf("%.1f TB", f)
}
