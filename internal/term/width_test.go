package term

import "testing"

// Width must never panic and must report a non-negative count. Under `go test`
// stdout is a pipe, not a TTY, so the ioctl fails and Width returns 0 — callers
// treat that as "unknown, use a fallback".
func TestWidthNonNegativeAndSafe(t *testing.T) {
	if w := Width(); w < 0 {
		t.Errorf("Width() = %d, want >= 0", w)
	}
}
