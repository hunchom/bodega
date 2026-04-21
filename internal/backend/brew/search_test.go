package brew

import (
	"testing"
)

func TestRankMatch(t *testing.T) {
	cases := []struct {
		name, q string
		want    int
		ok      bool
	}{
		{"vim", "vim", RankExact, true},
		{"VIM", "vim", RankExact, true}, // case-insensitive
		{"vimrc", "vim", RankPrefix, true},
		{"neovim", "vim", RankSubstring, true},
		{"emacs", "vim", 0, false},
		{"", "vim", 0, false},
		{"vim", "", 0, false},
	}
	for _, tc := range cases {
		r, ok := rankMatch(tc.name, tc.q)
		if ok != tc.ok || r != tc.want {
			t.Errorf("rankMatch(%q, %q) = (%d, %v), want (%d, %v)",
				tc.name, tc.q, r, ok, tc.want, tc.ok)
		}
	}
}

// fixtureFormulae is the shared corpus for SearchRich tests. Covers:
//   - exact/prefix/substring overlap on "vim"
//   - a desc-only hit ("neovim") — note: "vim" also matches neovim's name,
//     so description matching is exercised separately via "editor"
//   - a dep relationship (openssl@3 → curl depends on it)
//   - a tap-only hit (the homebrew/games tap)
func fixtureFormulae() map[string]*APIFormula {
	return map[string]*APIFormula{
		"vim": {
			Name:     "vim",
			FullName: "vim",
			Tap:      "homebrew/core",
			Desc:     "Vi 'workalike' with many additional features",
			Dependencies: []string{"ncurses"},
		},
		"vimpager": {
			Name:     "vimpager",
			FullName: "vimpager",
			Tap:      "homebrew/core",
			Desc:     "Use ViM as PAGER",
		},
		"neovim": {
			Name:     "neovim",
			FullName: "neovim",
			Tap:      "homebrew/core",
			Desc:     "Ambitious Vim-fork focused on extensibility and agility",
			Dependencies: []string{"libuv"},
		},
		"nano": {
			Name:     "nano",
			FullName: "nano",
			Tap:      "homebrew/core",
			Desc:     "Free (GNU) replacement for the Pico text editor",
		},
		"openssl@3": {
			Name:     "openssl@3",
			FullName: "openssl@3",
			Tap:      "homebrew/core",
			Desc:     "Cryptography and SSL/TLS Toolkit",
		},
		"curl": {
			Name:     "curl",
			FullName: "curl",
			Tap:      "homebrew/core",
			Desc:     "Get a file from an HTTP, HTTPS or FTP server",
			Dependencies: []string{"openssl@3", "zstd"},
		},
		"pacman": {
			Name:     "pacman",
			FullName: "pacman",
			Tap:      "homebrew/retro",
			Desc:     "Chomp chomp",
		},
	}
}

func TestSearchRichRanking(t *testing.T) {
	c := newAPICacheFromMaps(fixtureFormulae(), nil)
	results, err := c.SearchRich("vim", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 3 {
		t.Fatalf("expected 3+ results, got %d: %+v", len(results), results)
	}
	// Exact "vim" must rank first.
	if results[0].Pkg.Name != "vim" || results[0].Rank != RankExact {
		t.Errorf("expected vim exact first, got %+v", results[0])
	}
	// Prefix ("vimpager") beats substring ("neovim").
	var posPrefix, posSub int = -1, -1
	for i, r := range results {
		if r.Pkg.Name == "vimpager" {
			posPrefix = i
		}
		if r.Pkg.Name == "neovim" {
			posSub = i
		}
	}
	if posPrefix < 0 || posSub < 0 {
		t.Fatalf("missing expected rows: vimpager=%d neovim=%d all=%+v", posPrefix, posSub, results)
	}
	if posPrefix >= posSub {
		t.Errorf("prefix (vimpager @ %d) should precede substring (neovim @ %d)", posPrefix, posSub)
	}
	for _, r := range results {
		if r.Pkg.Name == "vimpager" && r.Rank != RankPrefix {
			t.Errorf("vimpager rank=%d want %d", r.Rank, RankPrefix)
		}
		if r.Pkg.Name == "neovim" && r.Rank != RankSubstring {
			t.Errorf("neovim rank=%d want %d", r.Rank, RankSubstring)
		}
	}
}

func TestSearchRichDedupNameOverDesc(t *testing.T) {
	// "vim" appears in the "vim" formula's name AND in its desc ("Vi
	// 'workalike'…" — actually no. Use a term that hits both: "Vim" is in
	// neovim's desc and also in its name.
	c := newAPICacheFromMaps(fixtureFormulae(), nil)
	results, err := c.SearchRich("neovim", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range results {
		if r.Pkg.Name == "neovim" {
			n++
			if r.MatchKind != MatchName {
				t.Errorf("neovim should match by name, got kind=%v", r.MatchKind)
			}
			if r.Rank != RankExact {
				t.Errorf("neovim rank=%d want %d", r.Rank, RankExact)
			}
		}
	}
	if n != 1 {
		t.Errorf("neovim appeared %d times, want 1", n)
	}
}

func TestSearchRichNameOnlySkipsDescAndTap(t *testing.T) {
	c := newAPICacheFromMaps(fixtureFormulae(), nil)

	// "editor" only hits nano's desc (and nothing's name/tap). NameOnly
	// should yield zero results.
	res, err := c.SearchRich("editor", SearchOpts{NameOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("NameOnly editor expected 0, got %d: %+v", len(res), res)
	}
	// Without NameOnly the desc match lands.
	res, err = c.SearchRich("editor", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res {
		if r.Pkg.Name == "nano" && r.MatchKind == MatchDesc {
			found = true
		}
	}
	if !found {
		t.Errorf("expected nano desc match, got %+v", res)
	}

	// Tap-only hit: "retro" only appears in pacman's tap.
	res, err = c.SearchRich("retro", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	foundTap := false
	for _, r := range res {
		if r.Pkg.Name == "pacman" && r.MatchKind == MatchTap {
			foundTap = true
		}
	}
	if !foundTap {
		t.Errorf("expected pacman tap match, got %+v", res)
	}

	// And NameOnly drops it.
	res, err = c.SearchRich("retro", SearchOpts{NameOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("NameOnly games expected 0, got %d: %+v", len(res), res)
	}
}

func TestSearchRichDeps(t *testing.T) {
	c := newAPICacheFromMaps(fixtureFormulae(), nil)

	// Without --deps, "openssl@3" only returns openssl@3 itself.
	res, err := c.SearchRich("openssl@3", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Pkg.Name == "curl" {
			t.Errorf("curl leaked into non-deps search: %+v", res)
		}
	}

	// With --deps, curl appears tagged as a dep match.
	res, err = c.SearchRich("openssl@3", SearchOpts{IncludeDeps: true})
	if err != nil {
		t.Fatal(err)
	}
	foundCurl := false
	for _, r := range res {
		if r.Pkg.Name == "curl" {
			foundCurl = true
			if r.MatchKind != MatchDep {
				t.Errorf("curl kind=%v want %v", r.MatchKind, MatchDep)
			}
			if r.Rank != RankDep {
				t.Errorf("curl rank=%d want %d", r.Rank, RankDep)
			}
		}
	}
	if !foundCurl {
		t.Errorf("expected curl via deps, got %+v", res)
	}

	// NameOnly disables deps even when IncludeDeps is set.
	res, err = c.SearchRich("openssl@3", SearchOpts{IncludeDeps: true, NameOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Pkg.Name == "curl" {
			t.Errorf("NameOnly+IncludeDeps should not expand: %+v", res)
		}
	}
}

func TestSearchRichLimit(t *testing.T) {
	c := newAPICacheFromMaps(fixtureFormulae(), nil)
	res, err := c.SearchRich("vim", SearchOpts{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 via Limit, got %d: %+v", len(res), res)
	}
	if res[0].Pkg.Name != "vim" {
		t.Errorf("top after limit is %q, want vim", res[0].Pkg.Name)
	}
}

func TestSearchRichEmptyQuery(t *testing.T) {
	c := newAPICacheFromMaps(fixtureFormulae(), nil)
	res, err := c.SearchRich("   ", SearchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("empty query should yield 0 results, got %d", len(res))
	}
}
