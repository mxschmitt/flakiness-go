//go:build linux

package telemetry

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// cpuTimes holds the cumulative tick counters for one CPU core.
type cpuTimes struct {
	total uint64
	busy  uint64
}

// utilizationSince returns the percentage [0,100] of time this core was busy
// between prev and c. A core that did no work returns 0.
func (c cpuTimes) utilizationSince(prev cpuTimes) float64 {
	dt := c.total - prev.total
	if dt == 0 {
		return 0
	}
	db := c.busy - prev.busy
	return float64(db) / float64(dt) * 100
}

// readCPU parses per-core lines from /proc/stat ("cpu0", "cpu1", …; the
// aggregate "cpu" line is skipped). Fields: user nice system idle iowait irq
// softirq steal guest guest_nice. busy = total - (idle + iowait).
func readCPU() ([]cpuTimes, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var out []cpuTimes
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			break // cpu lines are first; stop at the first non-cpu line
		}
		fields := strings.Fields(line)
		if fields[0] == "cpu" || len(fields) < 8 {
			continue // skip the aggregate line and malformed entries
		}
		// Match Node's os.cpus(), which the SDK reads: it exposes only
		// user, nice, sys, idle, irq — so total = user+nice+sys+irq+idle and
		// busy = total - idle. iowait/softirq/steal/guest are intentionally
		// excluded so our utilization numbers match the Node SDK on the same
		// host. /proc/stat order: user nice system idle iowait irq softirq ...
		nums := make([]uint64, 0, 8)
		for _, f := range fields[1:8] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				v = 0
			}
			nums = append(nums, v)
		}
		user, nice, system, idle, irq := nums[0], nums[1], nums[2], nums[3], nums[5]
		total := user + nice + system + irq + idle
		out = append(out, cpuTimes{total: total, busy: total - idle})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// availableMemory returns free memory in bytes. It uses MemFree to match
// Node's os.freemem() (which the SDK's RAMUtilization uses on Linux), so our
// RAM-utilization series matches the Node SDK on the same host. (MemAvailable
// would be more "htop-like" but diverges from the SDK.)
func availableMemory() (int64, bool) {
	if v, ok := meminfoKB("MemFree:"); ok {
		return v * 1024, true
	}
	return 0, false
}

func totalMemory() int64 {
	if v, ok := meminfoKB("MemTotal:"); ok {
		return v * 1024
	}
	return 0
}

func meminfoKB(key string) (int64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, key) {
			continue
		}
		fields := strings.Fields(line) // e.g. "MemTotal:  16384000 kB"
		if len(fields) < 2 {
			return 0, false
		}
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

func numCPU() int { return runtime.NumCPU() }
