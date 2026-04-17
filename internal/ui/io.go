package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type Writer struct {
	Out  io.Writer
	Err  io.Writer
	JSON bool
}

func NewWriter() *Writer {
	return &Writer{Out: os.Stdout, Err: os.Stderr}
}

func (w *Writer) IsTTY() bool {
	f, ok := w.Out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func (w *Writer) Println(a ...any)           { fmt.Fprintln(w.Out, a...) }
func (w *Writer) Printf(f string, a ...any)  { fmt.Fprintf(w.Out, f, a...) }
func (w *Writer) Errorln(a ...any)           { fmt.Fprintln(w.Err, a...) }
func (w *Writer) Errorf(f string, a ...any)  { fmt.Fprintf(w.Err, f, a...) }

func (w *Writer) Print(v any) error {
	if w.JSON {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w.Out, string(b))
		return err
	}
	_, err := fmt.Fprintln(w.Out, v)
	return err
}
