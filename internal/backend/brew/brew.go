package brew

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/runner"
)

type Brew struct {
	R runner.Runner
}

func New(r runner.Runner) *Brew { return &Brew{R: r} }

func (b *Brew) Name() string { return "brew" }

func (b *Brew) Search(ctx context.Context, q string) ([]backend.Package, error) {
	out, err := b.R.Run(ctx, "brew", "search", q)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("search", q, out)
	}
	var pkgs []backend.Package
	sc := bufio.NewScanner(strings.NewReader(string(out.Stdout)))
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" || strings.HasPrefix(name, "=") {
			continue
		}
		src := backend.SrcFormula
		if strings.HasSuffix(name, " (cask)") {
			src = backend.SrcCask
			name = strings.TrimSuffix(name, " (cask)")
		}
		pkgs = append(pkgs, backend.Package{Name: name, Source: src})
	}
	return pkgs, nil
}

func (b *Brew) Info(ctx context.Context, name string) (*backend.Package, error) {
	out, err := b.R.Run(ctx, "brew", "info", "--json=v2", name)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("info", name, out)
	}
	return parseInfoV2(out.Stdout, name)
}

func (b *Brew) List(ctx context.Context, f backend.ListFilter) ([]backend.Package, error) {
	switch f {
	case "", backend.ListInstalled:
		return b.parseListVersions(ctx, "--formula")
	case backend.ListCasks:
		return b.parseListVersions(ctx, "--cask")
	case backend.ListOutdated:
		return b.Outdated(ctx)
	case backend.ListLeaves:
		out, err := b.R.Run(ctx, "brew", "leaves")
		if err != nil {
			return nil, err
		}
		if out.ExitCode != 0 {
			return nil, brewErr("leaves", "", out)
		}
		return linesToPkgs(out.Stdout, backend.SrcFormula), nil
	case backend.ListPinned:
		out, err := b.R.Run(ctx, "brew", "list", "--pinned")
		if err != nil {
			return nil, err
		}
		if out.ExitCode != 0 {
			return nil, brewErr("list --pinned", "", out)
		}
		pkgs := linesToPkgs(out.Stdout, backend.SrcFormula)
		for i := range pkgs {
			pkgs[i].Pinned = true
		}
		return pkgs, nil
	case backend.ListAvailable:
		out, err := b.R.Run(ctx, "brew", "formulae")
		if err != nil {
			return nil, err
		}
		if out.ExitCode != 0 {
			return nil, brewErr("formulae", "", out)
		}
		return linesToPkgs(out.Stdout, backend.SrcFormula), nil
	}
	return nil, fmt.Errorf("unknown list filter: %q", f)
}

func (b *Brew) parseListVersions(ctx context.Context, flag string) ([]backend.Package, error) {
	out, err := b.R.Run(ctx, "brew", "list", flag, "--versions")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("list "+flag, "", out)
	}
	var pkgs []backend.Package
	sc := bufio.NewScanner(strings.NewReader(string(out.Stdout)))
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) == 0 {
			continue
		}
		p := backend.Package{Name: parts[0], Source: backend.SrcFormula}
		if flag == "--cask" {
			p.Source = backend.SrcCask
		}
		if len(parts) > 1 {
			p.Version = parts[len(parts)-1]
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func (b *Brew) Outdated(ctx context.Context) ([]backend.Package, error) {
	out, err := b.R.Run(ctx, "brew", "outdated", "--json=v2")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("outdated", "", out)
	}
	return parseOutdatedV2(out.Stdout)
}

func (b *Brew) Deps(ctx context.Context, name string) (*backend.DepTree, error) {
	out, err := b.R.Run(ctx, "brew", "deps", "--tree", name)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("deps", name, out)
	}
	return parseDepsTree(out.Stdout, name), nil
}

func (b *Brew) ReverseDeps(ctx context.Context, name string) ([]string, error) {
	out, err := b.R.Run(ctx, "brew", "uses", "--installed", name)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("uses", name, out)
	}
	var names []string
	for _, l := range strings.Split(string(out.Stdout), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			names = append(names, l)
		}
	}
	return names, nil
}

func (b *Brew) Provides(ctx context.Context, cmd string) ([]string, error) {
	out, err := b.R.Run(ctx, "brew", "which-formula", cmd)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("which-formula", cmd, out)
	}
	return strings.Fields(string(out.Stdout)), nil
}

func (b *Brew) Taps(ctx context.Context) ([]string, error) {
	out, err := b.R.Run(ctx, "brew", "tap")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("tap", "", out)
	}
	var taps []string
	for _, l := range strings.Split(string(out.Stdout), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			taps = append(taps, l)
		}
	}
	return taps, nil
}

func (b *Brew) Pin(ctx context.Context, name string, pin bool) error {
	cmd := "pin"
	if !pin {
		cmd = "unpin"
	}
	out, err := b.R.Run(ctx, "brew", cmd, name)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return brewErr(cmd, name, out)
	}
	return nil
}

func (b *Brew) Cleanup(ctx context.Context, deep bool) error {
	args := []string{"cleanup"}
	if deep {
		args = append(args, "--prune=all")
	}
	out, err := b.R.Run(ctx, "brew", args...)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return brewErr("cleanup", "", out)
	}
	return nil
}

func (b *Brew) Doctor(ctx context.Context) ([]string, error) {
	out, _ := b.R.Run(ctx, "brew", "doctor")
	var warns []string
	sc := bufio.NewScanner(strings.NewReader(string(out.Stdout)))
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "Warning:") || strings.HasPrefix(l, "Error:") {
			warns = append(warns, l)
		}
	}
	return warns, nil
}

func linesToPkgs(b []byte, src backend.Source) []backend.Package {
	var pkgs []backend.Package
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		pkgs = append(pkgs, backend.Package{Name: l, Source: src})
	}
	return pkgs
}

func (b *Brew) stream(ctx context.Context, w backend.ProgressWriter, args ...string) error {
	var sink io.Writer = io.Discard
	if w != nil {
		sink = w
	}
	r, err := b.R.Stream(ctx, sink, sink, "brew", args...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("brew %s: exit %d", args[0], r.ExitCode)
	}
	return nil
}

func (b *Brew) Install(ctx context.Context, names []string, w backend.ProgressWriter) error {
	return b.stream(ctx, w, append([]string{"install"}, names...)...)
}
func (b *Brew) Remove(ctx context.Context, names []string, w backend.ProgressWriter) error {
	return b.stream(ctx, w, append([]string{"uninstall"}, names...)...)
}
func (b *Brew) Reinstall(ctx context.Context, names []string, w backend.ProgressWriter) error {
	return b.stream(ctx, w, append([]string{"reinstall"}, names...)...)
}
func (b *Brew) Upgrade(ctx context.Context, names []string, w backend.ProgressWriter) error {
	return b.stream(ctx, w, append([]string{"upgrade"}, names...)...)
}
func (b *Brew) Autoremove(ctx context.Context, w backend.ProgressWriter) error {
	return b.stream(ctx, w, "autoremove")
}

// Helper JSON types (narrow — ignore fields we don't use).
type infoV2 struct {
	Formulae []struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Desc     string `json:"desc"`
		License  string `json:"license"`
		Homepage string `json:"homepage"`
		Tap      string `json:"tap"`
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
		Dependencies []string `json:"dependencies"`
		BuildDeps    []string `json:"build_dependencies"`
		Installed    []struct {
			Version string `json:"version"`
			Time    int64  `json:"time"`
		} `json:"installed"`
		Pinned bool `json:"pinned"`
	} `json:"formulae"`
	Casks []struct {
		Token    string   `json:"token"`
		Name     []string `json:"name"`
		Desc     string   `json:"desc"`
		Homepage string   `json:"homepage"`
		Version  string   `json:"version"`
		Tap      string   `json:"tap"`
	} `json:"casks"`
}

var _ = json.Unmarshal // keep the import visible in parse.go callers

// brewErr turns a non-zero brew invocation into a user-facing error. It prefers
// the last non-empty stderr line (which is usually the "Error: ..." message
// brew prints) and falls back to a canned message when stderr is empty.
func brewErr(sub, arg string, r *runner.Result) error {
	msg := ""
	for _, l := range strings.Split(string(r.Stderr), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			msg = l
		}
	}
	if msg == "" {
		for _, l := range strings.Split(string(r.Stdout), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				msg = l
			}
		}
	}
	if arg != "" {
		if msg == "" {
			return fmt.Errorf("brew %s %s: exit %d", sub, arg, r.ExitCode)
		}
		return fmt.Errorf("brew %s %s: %s", sub, arg, msg)
	}
	if msg == "" {
		return fmt.Errorf("brew %s: exit %d", sub, r.ExitCode)
	}
	return fmt.Errorf("brew %s: %s", sub, msg)
}
