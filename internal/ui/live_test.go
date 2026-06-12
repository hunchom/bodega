package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestStyleBrewLine(t *testing.T) {
	cases := []struct{ in, wantSub string }{
		{"==> Upgrading anaconda", "Upgrading anaconda"},
		{"==> Removing files:", "Removing files:"},
		{"Error: docker-desktop: It seems the App source is gone", "✗"},
		{"Warning: something", "⚠"},
		{"ghostty was successfully upgraded!", "✓"},
		{"Purging files for version 1.1.3 of Cask ghostty", "Purging"},
		{"auto-fix docker-desktop: app bundle missing — reinstalling cask", "⟳"},
		{"plain passthrough line", "plain passthrough line"},
	}
	for _, c := range cases {
		got := StyleBrewLine(c.in)
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("StyleBrewLine(%q) = %q, want substring %q", c.in, got, c.wantSub)
		}
	}
}

func TestLiveEventLifecycle(t *testing.T) {
	var buf bytes.Buffer
	l := NewLive(&buf)
	l.Event("resolve", "zstd", "1.5.7_1", 0, -1, "resolved zstd 1.5.7_1 (arm64)")
	l.Event("download", "zstd", "1.5.7_1", 512, 2048, "")
	l.Event("link", "zstd", "1.5.7_1", 0, -1, "linking zstd 1.5.7_1")
	l.Event("installed", "zstd", "1.5.7_1", 0, -1, "installed zstd 1.5.7_1")
	if _, err := l.Write([]byte("Error: boom\npartial")); err != nil {
		t.Fatal(err)
	}
	l.Close()

	out := buf.String()
	for _, want := range []string{"zstd", "✓", "✗", "partial", "resolved zstd"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Closed renderer leaves no dangling in-flight block.
	if l.drawn != 0 {
		t.Errorf("drawn=%d after Close", l.drawn)
	}
}
