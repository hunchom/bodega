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
}

func (f *Fake) key(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

func (f *Fake) Run(_ context.Context, name string, args ...string) (*Result, error) {
	f.Calls = append(f.Calls, FakeCall{Name: name, Args: args})
	k := f.key(name, args)
	return &Result{
		Stdout:   []byte(f.Stdout[k]),
		Stderr:   []byte(f.Stderr[k]),
		ExitCode: f.ExitCode[k],
	}, nil
}

func (f *Fake) Stream(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (*Result, error) {
	r, err := f.Run(ctx, name, args...)
	stdout.Write(r.Stdout)
	stderr.Write(r.Stderr)
	return r, err
}
