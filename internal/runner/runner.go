package runner

import (
	"bytes"
	"context"
	"io"
	"os/exec"
)

type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Runner runs external commands. The interface exists so tests can swap in Fake.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (*Result, error)
	Stream(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (*Result, error)
}

type Real struct{}

func (Real) Run(ctx context.Context, name string, args ...string) (*Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	r := &Result{Stdout: out.Bytes(), Stderr: errb.Bytes(), ExitCode: 0}
	if ee, ok := err.(*exec.ExitError); ok {
		r.ExitCode = ee.ExitCode()
		return r, nil
	}
	return r, err
}

func (Real) Stream(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) (*Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	r := &Result{}
	if ee, ok := err.(*exec.ExitError); ok {
		r.ExitCode = ee.ExitCode()
		return r, nil
	}
	return r, err
}
