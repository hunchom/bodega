package cmd

import (
	"reflect"
	"testing"
)

func TestParseFreedSize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"==> This operation has freed approximately 142.5MB of disk space.\n", "142.5MB"},
		{"random chatter\n==> This operation has freed approximately 1.2GB of disk space.\n", "1.2GB"},
		{"nothing interesting here\n", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := parseFreedSize([]byte(tc.in))
		if got != tc.want {
			t.Errorf("parseFreedSize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseUninstalled(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "path_form",
			in: `==> Autoremoving 2 unneeded formulae:
foo bar
Uninstalling /opt/homebrew/Cellar/foo/1.2.3... (42 files, 1.1MB)
Uninstalling /opt/homebrew/Cellar/bar/0.1.0... (5 files, 12KB)
`,
			want: []string{"foo", "bar"},
		},
		{
			name: "bare_form",
			in: `Uninstalling baz... (1 files, 1KB)
`,
			want: []string{"baz"},
		},
		{
			name: "dedup",
			in: `Uninstalling /opt/homebrew/Cellar/foo/1.0.0... (x files, y)
Uninstalling /opt/homebrew/Cellar/foo/1.0.0... (x files, y)
`,
			want: []string{"foo"},
		},
		{
			name: "no_match",
			in: `Warning: deps are fine
`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseUninstalled([]byte(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseUninstalled() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
