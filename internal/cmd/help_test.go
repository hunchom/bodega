package cmd

import (
	"bytes"
	"os"
	"testing"
	"unicode/utf8"
)

func TestHelpRoot(t *testing.T) {
	root := NewRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("testdata/help_root.txt")
	if buf.String() != string(want) {
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile("testdata/help_root.txt", buf.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatalf("help mismatch — run with UPDATE_GOLDEN=1 to accept\n%s", buf.String())
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	// Multibyte em-dash straddling the cut boundary must not be sliced
	// mid-rune. n is a rune count, not a byte count.
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 50, "short"},
		{"abcdef", 4, "abc…"},
		{"日本語テスト", 4, "日本語…"}, // CJK: 3 runes + ellipsis, never half a rune
		{"a—b—c—d—e—f", 5, "a—b—…"},
		{"", 10, ""},
		{"x", 0, ""},
	}
	for _, c := range cases {
		got := truncate(c.s, c.n)
		if got != c.want {
			t.Errorf("truncate(%q,%d)=%q want %q", c.s, c.n, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q,%d) produced invalid UTF-8: %q", c.s, c.n, got)
		}
	}
}
