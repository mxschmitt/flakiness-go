//go:build !linux

package telemetry

import "runtime"

// cpuTimes/readCPU/availableMemory are unimplemented off Linux. The collector
// degrades to producing no telemetry, which is a documented no-op.

type cpuTimes struct{ total, busy uint64 }

func (c cpuTimes) utilizationSince(prev cpuTimes) float64 { return 0 }

func readCPU() ([]cpuTimes, bool) { return nil, false }

func availableMemory() (int64, bool) { return 0, false }

func totalMemory() int64 { return 0 }

func numCPU() int { return runtime.NumCPU() }
