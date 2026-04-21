package ui

import "fmt"

// HumanBytes renders a byte count as a short human-readable string
// ("1.5 MB", "3.2 GB"). Binary prefixes (1024-based) to match the rest
// of the filesystem tooling.
func HumanBytes(n int64) string {
	const k = 1024.0
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if f < k {
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= k
	}
	return fmt.Sprintf("%.1f TB", f)
}
