//go:build !windows

package cli

// DoubleClickLaunch is a Windows-only concept; other platforms are always
// launched from a shell, a service manager or a supervisor.
func DoubleClickLaunch() bool { return false }

// HoldConsoleOnExit never blocks outside Windows.
func HoldConsoleOnExit(int) {}

// OpenBrowser is a no-op outside Windows. Scripts on Linux/macOS own the
// decision of whether opening a browser is appropriate.
func OpenBrowser(string) error { return nil }
