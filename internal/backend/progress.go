package backend

import "io"

type StreamPW struct {
	W      io.Writer
	OnStep func(string)
}

func (p *StreamPW) Write(b []byte) (int, error) { return p.W.Write(b) }
func (p *StreamPW) Step(msg string) {
	if p.OnStep != nil {
		p.OnStep(msg)
	}
}
