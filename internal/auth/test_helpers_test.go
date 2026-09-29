package auth

import (
	"path/filepath"
	"testing"
)

// privateTestDir returns a path that does not exist yet. Store.Save creates it
// with mode 0700, matching production behavior on Unix. t.TempDir itself can
// be 0755 on CI runners, which is intentionally rejected for credential data.
func privateTestDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "mys")
}
