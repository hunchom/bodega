package cmd

import (
	"github.com/hunchom/bodega/internal/ui"
)

// livePW bridges the Live renderer to the backend: raw brew subprocess
// output arrives via Write (restyled line-by-line), native-install progress
// arrives structured via InstallEvent (spinners + byte bars).
type livePW struct{ L *ui.Live }

func (p *livePW) Write(b []byte) (int, error) { return p.L.Write(b) }
func (p *livePW) Step(string)                 {}
func (p *livePW) InstallEvent(phase, pkg, version string, current, total int64, message string) {
	p.L.Event(phase, pkg, version, current, total, message)
}
