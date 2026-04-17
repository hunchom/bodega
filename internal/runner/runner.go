package runner

import (
	"bytes"
	"context"
	"io"
	"os"
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

// brewEnv is the set of HOMEBREW_* knobs we force in every subprocess we
// spawn. Most of them suppress work that the yum CLI never wants: auto-update
// during install/upgrade (we handle tap refresh ourselves; see brew/refresh.go),
// the one-shot usage hints brew prints to stderr, emoji progress indicators,
// and the analytics ping. On a cold run these easily cost hundreds of
// milliseconds per `brew` invocation.
var brewEnv = []string{
	"HOMEBREW_NO_AUTO_UPDATE=1",
	"HOMEBREW_NO_ANALYTICS=1",
	"HOMEBREW_NO_ENV_HINTS=1",
	"HOMEBREW_NO_EMOJI=1",
}

// envForBrew returns the caller's environment with our brew knobs appended
// when the child command is "brew". For anything else we leave the env
// untouched (nil tells exec to inherit as usual).
func envForBrew(name string) []string {
	if name != "brew" {
		return nil
	}
	return append(os.Environ(), brewEnv...)
}

type Real struct{}

func (Real) Run(ctx context.Context, name string, args ...string) (*Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env := envForBrew(name); env != nil {
		cmd.Env = env
	}
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
	if env := envForBrew(name); env != nil {
		cmd.Env = env
	}
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
