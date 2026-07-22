// Package buildinfo carries the build-time version of ytdl.
package buildinfo

// Version is the ytdl version. It defaults to "dev" for local builds and is
// overwritten at release time with the git tag via the linker:
//
//	-ldflags "-X github.com/alergyonthestage/ytdl/internal/buildinfo.Version=v2.0.0"
//
// The Go engine versioning starts at 2.0.0 — a clean break from the Bash 1.x
// line (see design-cycle1-remaining.md §4.3).
var Version = "dev"
