package brew

import (
	"bufio"
	"encoding/json"
	"strings"
	"time"

	"github.com/hunchom/yum/internal/backend"
)

func parseInfoV2(b []byte, want string) (*backend.Package, error) {
	var v infoV2
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	if len(v.Formulae) > 0 {
		f := v.Formulae[0]
		p := &backend.Package{
			Name: f.Name, Source: backend.SrcFormula, Desc: f.Desc,
			License: f.License, Homepage: f.Homepage, Tap: f.Tap,
			Version: f.Versions.Stable, Deps: f.Dependencies, BuildDeps: f.BuildDeps,
			Pinned: f.Pinned,
		}
		if len(f.Installed) > 0 {
			p.Version = f.Installed[0].Version
			p.InstalledOn = time.Unix(f.Installed[0].Time, 0)
		}
		return p, nil
	}
	if len(v.Casks) > 0 {
		c := v.Casks[0]
		p := &backend.Package{
			Name: c.Token, Source: backend.SrcCask,
			Desc: c.Desc, Homepage: c.Homepage, Version: c.Version, Tap: c.Tap,
		}
		if len(c.Name) > 0 && p.Desc == "" {
			p.Desc = c.Name[0]
		}
		return p, nil
	}
	return nil, &notFoundErr{name: want}
}

type notFoundErr struct{ name string }

func (e *notFoundErr) Error() string { return "package not found: " + e.name }

type outdatedV2 struct {
	Formulae []struct {
		Name              string   `json:"name"`
		InstalledVersions []string `json:"installed_versions"`
		CurrentVersion    string   `json:"current_version"`
		Pinned            bool     `json:"pinned"`
	} `json:"formulae"`
	Casks []struct {
		Name              string `json:"name"`
		InstalledVersions string `json:"installed_versions"`
		CurrentVersion    string `json:"current_version"`
	} `json:"casks"`
}

func parseOutdatedV2(b []byte) ([]backend.Package, error) {
	var v outdatedV2
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	var out []backend.Package
	for _, f := range v.Formulae {
		cur := ""
		if len(f.InstalledVersions) > 0 {
			cur = f.InstalledVersions[0]
		}
		out = append(out, backend.Package{
			Name: f.Name, Version: cur, Latest: f.CurrentVersion,
			Source: backend.SrcFormula, Pinned: f.Pinned,
		})
	}
	for _, c := range v.Casks {
		out = append(out, backend.Package{
			Name: c.Name, Version: c.InstalledVersions, Latest: c.CurrentVersion,
			Source: backend.SrcCask,
		})
	}
	return out, nil
}

// parseDepsTree reads `brew deps --tree` indented output.
func parseDepsTree(b []byte, root string) *backend.DepTree {
	r := &backend.DepTree{Name: root}
	stack := []*backend.DepTree{r}
	depths := []int{-1}

	sc := bufio.NewScanner(strings.NewReader(string(b)))
	firstLineConsumed := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !firstLineConsumed {
			// First line is the root name itself — skip.
			firstLineConsumed = true
			if strings.TrimSpace(line) == root {
				continue
			}
		}
		// count leading spaces before any ├ └ ─ │ glyphs
		depth := 0
		for _, r := range line {
			if r == ' ' || r == '│' || r == '├' || r == '└' || r == '─' {
				depth++
				continue
			}
			break
		}
		name := strings.TrimLeft(line, " │├└─")
		node := &backend.DepTree{Name: name}
		for len(depths) > 1 && depth <= depths[len(depths)-1] {
			stack = stack[:len(stack)-1]
			depths = depths[:len(depths)-1]
		}
		stack[len(stack)-1].Children = append(stack[len(stack)-1].Children, node)
		stack = append(stack, node)
		depths = append(depths, depth)
	}
	return r
}
