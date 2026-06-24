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
		if fields[0] == "cpu" || len(fields) < 5 {
			continue // skip the aggregate line and malformed entries
		}
		var total, idle uint64
		for i, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				continue
			}
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		out = append(out, cpuTimes{total: total, busy: total - idle})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// availableMemory returns MemAvailable from /proc/meminfo in bytes.
func availableMemory() (int64, bool) {
	if v, ok := meminfoKB("MemAvailable:"); ok {
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
