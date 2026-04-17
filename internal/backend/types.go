package backend

import (
	"context"
	"time"
)

type Source string

const (
	SrcFormula Source = "formula"
	SrcCask    Source = "cask"
	SrcMas     Source = "mas"
)

type Package struct {
	Name        string     `json:"name"`
	Version     string     `json:"version,omitempty"`
	Latest      string     `json:"latest,omitempty"`
	Source      Source     `json:"source"`
	Tap         string     `json:"tap,omitempty"`
	Desc        string     `json:"desc,omitempty"`
	Homepage    string     `json:"homepage,omitempty"`
	License     string     `json:"license,omitempty"`
	Size        int64      `json:"size,omitempty"`
	InstalledOn *time.Time `json:"installed_on,omitempty"`
	Pinned      bool       `json:"pinned,omitempty"`
	Deps        []string   `json:"deps,omitempty"`
	BuildDeps   []string   `json:"build_deps,omitempty"`
}

type ListFilter string

const (
	ListInstalled ListFilter = "installed"
	ListAvailable ListFilter = "available"
	ListOutdated  ListFilter = "outdated"
	ListLeaves    ListFilter = "leaves"
	ListPinned    ListFilter = "pinned"
	ListCasks     ListFilter = "casks"
)

type DepTree struct {
	Name     string
	Children []*DepTree
}

type ProgressWriter interface {
	Write(p []byte) (int, error)
	Step(msg string)
}

type Backend interface {
	Name() string
	Search(ctx context.Context, query string) ([]Package, error)
	Info(ctx context.Context, name string) (*Package, error)
	List(ctx context.Context, f ListFilter) ([]Package, error)
	Install(ctx context.Context, names []string, w ProgressWriter) error
	Remove(ctx context.Context, names []string, w ProgressWriter) error
	Reinstall(ctx context.Context, names []string, w ProgressWriter) error
	Upgrade(ctx context.Context, names []string, w ProgressWriter) error
	Outdated(ctx context.Context) ([]Package, error)
	Deps(ctx context.Context, name string) (*DepTree, error)
	ReverseDeps(ctx context.Context, name string) ([]string, error)
	Pin(ctx context.Context, name string, pin bool) error
	Cleanup(ctx context.Context, deep bool) error
	Autoremove(ctx context.Context, w ProgressWriter) error
	Taps(ctx context.Context) ([]string, error)
	Provides(ctx context.Context, cmd string) ([]string, error)
	Doctor(ctx context.Context) ([]string, error)
}
