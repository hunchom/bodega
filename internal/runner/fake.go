package runner

import (
	"context"
	"io"
	"strings"
)

type FakeCall struct {
	Name string
	Args []string
}

type Fake struct {
	Calls    []FakeCall
	Stdout   map[string]string // key: "name arg1 arg2"
	Stderr   map[string]string
	ExitCode map[string]int

	// *Once maps are consumed by the first call with that key; later calls
	// fall back to the regular maps. Models fail-then-recover sequences
	// (e.g. an upgrade that errors once, then a clean verify re-run).
	StderrOnce   map[string]string
	ExitCodeOnce map[string]int
}

func (f *Fake) key(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

func (f *Fake) Run(_ context.Context, name string, args ...string) (*Result, error) {
	f.Calls = append(f.Calls, FakeCall{Name: name, Args: args})
	k := f.key(name, args)
	r := &Result{
		Stdout:   []byte(f.Stdout[k]),
		Stderr:   []byte(f.Stderr[k]),
		ExitCode: f.ExitCode[k],
	}
	if s, ok := f.StderrOnce[k]; ok {
		r.Stderr = []byte(s)
		delete(f.StderrOnce, k)
	}
	if c, ok := f.ExitCodeOnce[k]; ok {
		r.ExitCode = c
		delete(f.ExitCodeOnce, k)
	}
	return r, nil
}

func (f *Fake) Stream(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (*Result, error) {
	r, err := f.Run(ctx, name, args...)
	stdout.Write(r.Stdout)
	stderr.Write(r.Stderr)
	return r, err
}
