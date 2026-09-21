package buildinfo

import "testing"

func TestString(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldVersion, oldCommit, oldDate })

	Version, Commit, Date = "1.2.3", "abc123", "2026-09-21T08:00:00Z"
	if got, want := String(), "1.2.3 (commit abc123, built 2026-09-21T08:00:00Z)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}

	Version, Commit, Date = "", "none", "unknown"
	if got := String(); got != "dev" {
		t.Fatalf("empty metadata String() = %q, want dev", got)
	}
}
