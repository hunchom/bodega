package brew

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// rbDependsOnRe pulls the "foo" out of `depends_on "foo"` and optionally
// captures the `=> :build | :test | [:build, :test]` suffix so callers can
// decide whether the dep is runtime-relevant. Homebrew uses other arrow
// targets too (`:recommended`, `:optional`); those *are* runtime unless
// explicitly excluded at install time, so we keep them.
var rbDependsOnRe = regexp.MustCompile(`(?m)^\s*depends_on\s+"([^"]+)"(?:\s*=>\s*(:[a-z]+|\[[^\]]+\]))?`)

// rbOnLinuxRe matches a full `on_linux do ... end` block, non-greedy, so we
// can drop deps gated to Linux before scanning for depends_on. bodega
// currently targets macOS; on a Linux host you'd want the reverse, but the
// codebase has no Linux build path today.
var rbOnLinuxRe = regexp.MustCompile(`(?s)\bon_linux\s+do\b.*?\n\s*end\b`)

// ParseRuntimeDeps reads $prefix/Cellar/<name>/<newest>/.brew/<name>.rb and
// returns the declared runtime dependencies. :build / :test arrow forms are
// filtered out; on_linux blocks are stripped before matching so Linux-only
// deps don't appear on macOS runs.
//
// The second return value reports whether the .brew rb file was actually
// found and parsed. A false value means "no local source of truth — fall
// back to an API-based resolver"; a true value with an empty slice means
// "rb file parsed, package genuinely has no runtime deps". The error slot
// is reserved for future stricter parsing; today all I/O failures surface
// as found=false.
func ParseRuntimeDeps(prefix, name string) ([]string, bool, error) {
	cellar := filepath.Join(prefix, "Cellar", name)
	versions, err := os.ReadDir(cellar)
	if err != nil {
		return nil, false, nil
	}
	var best string
	for _, v := range versions {
		n := v.Name()
		if !v.IsDir() || strings.HasPrefix(n, ".") {
			continue
		}
		if n > best {
			best = n
		}
	}
	if best == "" {
		return nil, false, nil
	}
	rb := filepath.Join(cellar, best, ".brew", name+".rb")
	data, err := os.ReadFile(rb)
	if err != nil {
		return nil, false, nil
	}
	cleaned := rbOnLinuxRe.ReplaceAllString(string(data), "")
	out := []string{}
	for _, m := range rbDependsOnRe.FindAllStringSubmatch(cleaned, -1) {
		if isRbNonRuntimeArrow(m[2]) {
			continue
		}
		out = append(out, m[1])
	}
	return out, true, nil
}

// isRbNonRuntimeArrow returns true when the `=> ...` suffix on a depends_on
// line marks the dep as build- or test-only. Accepts bare symbols
// (`:build`) and array forms (`[:build, :test]`). A dep tagged build AND
// test but nothing else is not needed at runtime; anything else (e.g. a
// bare `:recommended`, or a mixed array including `:build`) still counts as
// runtime.
func isRbNonRuntimeArrow(arrow string) bool {
	arrow = strings.TrimSpace(arrow)
	if arrow == "" {
		return false
	}
	if arrow == ":build" || arrow == ":test" {
		return true
	}
	if strings.HasPrefix(arrow, "[") && strings.HasSuffix(arrow, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(arrow, "["), "]")
		for _, tok := range strings.Split(inner, ",") {
			tok = strings.TrimSpace(tok)
			if tok != ":build" && tok != ":test" {
				return false
			}
		}
		return true
	}
	return false
}
