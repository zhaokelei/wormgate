//go:build windows

package sysinfo

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = modkernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")
)

// readCPU calls GetSystemTimes, whose kernel time already includes idle time —
// leaving us with total = kernel + user and idle reported separately.
func readCPU() (cpuSample, bool) {
	var idle, kernel, user windows.Filetime
	ret, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return cpuSample{}, false
	}
	toTicks := func(ft windows.Filetime) uint64 {
		return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
	}
	idleTicks := toTicks(idle)
	total := toTicks(kernel) + toTicks(user) // kernel already contains idle
	if total == 0 || idleTicks > total {
		return cpuSample{}, false
	}
	return cpuSample{busy: total - idleTicks, total: total}, true
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func readMem() (total, used uint64, ok bool) {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 || status.TotalPhys == 0 {
		return 0, 0, false
	}
	avail := status.AvailPhys
	if avail > status.TotalPhys {
		avail = status.TotalPhys
	}
	return status.TotalPhys, status.TotalPhys - avail, true
}
