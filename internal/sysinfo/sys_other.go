//go:build !linux && !windows

package sysinfo

// Unsupported platforms report Available=false so the console hides the host
// widgets instead of drawing a permanently empty 0% bar.
func readCPU() (cpuSample, bool) { return cpuSample{}, false }

func readMem() (total, used uint64, ok bool) { return 0, 0, false }
