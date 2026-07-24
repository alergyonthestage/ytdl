//go:build unix

package term

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// winsize mirrors struct winsize (sys/ioctl.h): ws_col is the second field.
type winsize struct {
	row, col, xpixel, ypixel uint16
}

// tiocgwinsz is the "get window size" ioctl request number. It differs between the
// BSD-derived (macOS) and Linux kernels. The supported build matrix is darwin and
// linux on amd64/arm64, where these two constants are correct; a few exotic Linux
// arches (mips, ppc64) actually use the BSD value, so this would mis-probe there —
// harmless (Width returns a wrong-but-bounded number, or the caller's 80 fallback),
// and those arches are not targets. If the matrix ever widens, switch on GOARCH too.
func tiocgwinsz() uintptr {
	if runtime.GOOS == "darwin" {
		return 0x40087468
	}
	return 0x5413 // linux (386/amd64/arm/arm64)
}

// Width returns stdout's terminal column count, or 0 when it cannot be determined
// (stdout is not a TTY, or the ioctl fails). Standard-library only — a raw
// TIOCGWINSZ ioctl — so the single static binary keeps no external dependency
// (ADR-0003). Callers pick their own fallback width when this is 0.
func Width() int {
	ws := &winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		tiocgwinsz(),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 {
		return 0
	}
	return int(ws.col)
}
