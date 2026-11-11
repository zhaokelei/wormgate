//go:build linux

package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// readCPU parses the aggregate "cpu" line of /proc/stat. Fields are, in order:
// user nice system idle iowait irq softirq steal guest guest_nice. guest time
// is already accounted for in user/nice, so the first eight fields are summed.
func readCPU() (cpuSample, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return cpuSample{}, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, false
	}
	var values []uint64
	for _, raw := range fields[1:] {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return cpuSample{}, false
		}
		values = append(values, v)
	}
	if len(values) > 8 {
		values = values[:8]
	}
	var total uint64
	for _, v := range values {
		total += v
	}
	// idle + iowait both count as not-busy.
	var idle uint64
	if len(values) > 3 {
		idle = values[3]
	}
	if len(values) > 4 {
		idle += values[4]
	}
	if total == 0 {
		return cpuSample{}, false
	}
	return cpuSample{busy: total - idle, total: total}, true
}

// readMem parses /proc/meminfo. MemAvailable is the kernel's own estimate of
// what a new workload could claim, which is far more useful than MemFree.
func readMem() (total, used uint64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	var avail uint64
	var haveAvail bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, rest, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}
		value, err := meminfoBytes(rest)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = value
		case "MemAvailable":
			avail, haveAvail = value, true
		}
	}
	if total == 0 {
		return 0, 0, false
	}
	if !haveAvail {
		return total, 0, true
	}
	if avail > total {
		avail = total
	}
	return total, total - avail, true
}

// meminfoBytes converts " 16384000 kB" to bytes.
func meminfoBytes(raw string) (uint64, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, strconv.ErrSyntax
	}
	v, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
		v *= 1024
	}
	return v, nil
}
