//go:build windows

package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// DoubleClickLaunch reports whether this process owns a console that Windows
// created just for it. That is what happens when explorer.exe launches an exe
// by double-click; a process started from cmd / PowerShell / Windows Terminal
// shares the parent console instead.
func DoubleClickLaunch() bool {
	var pids [64]uint32
	r, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return r == 1
}

// HoldConsoleOnExit keeps a double-clicked window open after a failure, so the
// operator can actually read the error instead of seeing a window flash away.
// It is a no-op for terminal, service and CI launches, which must never block.
func HoldConsoleOnExit(code int) {
	if code == 0 || !DoubleClickLaunch() {
		return
	}
	fmt.Fprintln(os.Stderr, "\nWormgate stopped with an error. Press Enter to close this window...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// OpenBrowser hands the URL to the user's default browser. rundll32 avoids the
// extra console window that `cmd /c start` would flash on screen.
func OpenBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
