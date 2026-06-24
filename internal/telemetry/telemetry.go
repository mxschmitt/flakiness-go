// Package telemetry samples system CPU and RAM utilization during a test run
// and enriches a report with the collected time series. It mirrors the Node
// SDK's CPUUtilization/RAMUtilization: average and peak per-core CPU, RAM as a
// percentage of total memory, flat-region coalescing, and delta-encoded
// timestamps.
//
// Sampling is only implemented where the OS exposes the necessary counters
// (currently Linux, via /proc). On other platforms reading returns ok=false and
// the collector simply produces no telemetry — a documented, harmless no-op.
package telemetry

import "github.com/mxschmitt/flakiness-go/report"

// Precision is the minimum change in percentage points required to record a new
// point; flatter movement collapses into the previous point. The Node SDK uses
// 10 for both series in its reporters.
const Precision = 10.0

type point struct {
	ts    int64 // unix ms
	value float64
}

// Collector accumulates CPU and RAM samples. It is not safe for concurrent use;
// call Sample from a single goroutine (see Sampler).
type Collector struct {
	lastCPU  []cpuTimes
	cpuOK    bool
	totalRAM int64

	cpuAvg []point
	cpuMax []point
	ram    []point
}

// NewCollector primes the baseline CPU sample and total memory.
func NewCollector() *Collector {
	c := &Collector{}
	c.lastCPU, c.cpuOK = readCPU()
	c.totalRAM = totalMemory()
	return c
}

// Sample records one CPU and RAM observation at time nowMS (unix ms). The first
// CPU sample only primes the baseline.
func (c *Collector) Sample(nowMS int64) {
	if cur, ok := readCPU(); ok && c.cpuOK && len(cur) == len(c.lastCPU) {
		var sum, max float64
		for i := range cur {
			u := cur[i].utilizationSince(c.lastCPU[i])
			sum += u
			if u > max {
				max = u
			}
		}
		add(&c.cpuAvg, point{nowMS, sum / float64(len(cur))}, Precision)
		add(&c.cpuMax, point{nowMS, max}, Precision)
		c.lastCPU = cur
	} else if ok {
		c.lastCPU = cur
		c.cpuOK = true
	}

	if c.totalRAM > 0 {
		if free, ok := availableMemory(); ok {
			used := float64(c.totalRAM-free) / float64(c.totalRAM) * 100
			add(&c.ram, point{nowMS, used}, Precision)
		}
	}
}

// Enrich writes the collected telemetry onto the report. It is a no-op for any
// series that has no samples.
func (c *Collector) Enrich(rep *report.Report) {
	if n := numCPU(); n > 0 {
		rep.CPUCount = n
	}
	if len(c.cpuAvg) > 0 {
		rep.CPUAvg = encode(c.cpuAvg)
	}
	if len(c.cpuMax) > 0 {
		rep.CPUMax = encode(c.cpuMax)
	}
	if c.totalRAM > 0 {
		rep.RAMBytes = c.totalRAM
	}
	if len(c.ram) > 0 {
		rep.RAM = encode(c.ram)
	}
}

// add appends p, but if the last two points and p are all within precision of
// each other it just extends the last point's timestamp (flat-region merge),
// matching the SDK's addTelemetryPoint.
func add(series *[]point, p point, precision float64) {
	s := *series
	if n := len(s); n >= 2 {
		last, prev := s[n-1], s[n-2]
		if abs(last.value-prev.value) < precision && abs(last.value-p.value) < precision {
			s[n-1].ts = p.ts
			return
		}
	}
	*series = append(s, p)
}

// encode delta-encodes timestamps and rounds values to 2 decimals, matching
// toProtocolTelemetry: first tuple is absolute, the rest are ms deltas.
func encode(s []point) []report.TelemetryPoint {
	if len(s) == 0 {
		return nil
	}
	out := make([]report.TelemetryPoint, len(s))
	last := s[0].ts
	for i, p := range s {
		var t float64
		if i == 0 {
			t = float64(p.ts)
		} else {
			t = float64(p.ts - last)
		}
		last = p.ts
		out[i] = report.TelemetryPoint{t, round2(p.value)}
	}
	return out
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
