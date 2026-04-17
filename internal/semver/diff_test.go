package semver

import "testing"

func TestDiff(t *testing.T) {
	cases := []struct {
		a, b string
		k    Kind
	}{
		{"1.2.3", "2.0.0", Major},
		{"1.2.3", "1.3.0", Minor},
		{"1.2.3", "1.2.4", Patch},
		{"1.2.3", "1.2.3", Unknown},
		{"abc", "1.2.3", Unknown},
	}
	for _, c := range cases {
		if got := Diff(c.a, c.b); got != c.k {
			t.Errorf("Diff(%s,%s)=%v want %v", c.a, c.b, got, c.k)
		}
	}
}
