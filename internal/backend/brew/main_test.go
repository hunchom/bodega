package brew

import (
	"os"
	"testing"
)

// TestMain disables the native index by default so no test touches the host's
// real ~/.local/share/yum/index.db or builds one over the network. Tests that
// exercise native-index paths inject a fixture via testIndexOverride (which
// takes precedence over indexDisabled).
func TestMain(m *testing.M) {
	indexDisabled = true
	os.Exit(m.Run())
}
