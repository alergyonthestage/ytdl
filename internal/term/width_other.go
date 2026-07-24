//go:build !unix

package term

// Width is unavailable off unix (e.g. a future Windows build): report 0 so callers
// fall back to a fixed width. Kept tiny so the rest of the package is portable.
func Width() int { return 0 }
