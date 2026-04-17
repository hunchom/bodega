package cmd

import (
	"bytes"
	"os"
	"testing"
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
