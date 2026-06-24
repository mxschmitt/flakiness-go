package telemetry

import (
	"runtime"
	"testing"

	"github.com/mxschmitt/flakiness-go/report"
)

func TestAdd_CoalescesFlatRegion(t *testing.T) {
	var s []point
	// Three near-equal values (within cpuPrecision) collapse to one point whose
	// timestamp advances to the latest.
	add(&s, point{1000, 50}, cpuPrecision)
	add(&s, point{2000, 51}, cpuPrecision)
	add(&s, point{3000, 52}, cpuPrecision)
	if len(s) != 2 {
		t.Fatalf("flat region should coalesce to 2 points, got %d: %+v", len(s), s)
	}
	if s[1].ts != 3000 {
		t.Errorf("last point ts = %d, want 3000 (advanced)", s[1].ts)
	}
}

func TestAdd_KeepsLargeSwings(t *testing.T) {
	var s []point
	add(&s, point{1000, 10}, cpuPrecision)
	add(&s, point{2000, 90}, cpuPrecision) // jump >> precision
	add(&s, point{3000, 12}, cpuPrecision) // jump back
	if len(s) != 3 {
		t.Fatalf("large swings must be kept, got %d points", len(s))
	}
}

func TestEncode_DeltaTimestampsAndRounding(t *testing.T) {
	s := []point{
		{1000, 25.0},
		{2000, 30.126},
		{2500, 28.0},
	}
	got := encode(s)
	if len(got) != 3 {
		t.Fatalf("want 3 tuples, got %d", len(got))
	}
	// First is absolute, rest are deltas.
	if got[0][0] != 1000 {
		t.Errorf("first ts = %v, want absolute 1000", got[0][0])
	}
	if got[1][0] != 1000 { // 2000-1000
		t.Errorf("second delta = %v, want 1000", got[1][0])
	}
	if got[2][0] != 500 { // 2500-2000
		t.Errorf("third delta = %v, want 500", got[2][0])
	}
	if got[1][1] != 30.13 { // rounded to 2 decimals
		t.Errorf("value rounding = %v, want 30.13", got[1][1])
	}
}

func TestRound2(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{30.126, 30.13},
		{30.124, 30.12},
		{30.125, 30.13}, // half away from zero
		{0, 0},
		{100, 100},
		{-30.126, -30.13}, // negatives round away from zero like Math.round's magnitude
	}
	for _, c := range cases {
		if got := round2(c.in); got != c.want {
			t.Errorf("round2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestClampPct(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{-0.5, 0},
		{0, 0},
		{50, 50},
		{100, 100},
		{100.01, 100}, // counter jitter must not exceed the schema's max
	} {
		if got := clampPct(c.in); got != c.want {
			t.Errorf("clampPct(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestEncode_Empty(t *testing.T) {
	if got := encode(nil); got != nil {
		t.Errorf("encode(nil) = %v, want nil", got)
	}
}

func TestCollector_EnrichSetsCPUCount(t *testing.T) {
	c := NewCollector()
	c.Sample(1000)
	c.Sample(2000)
	var rep report.Report
	c.Enrich(&rep)
	if rep.CPUCount != runtime.NumCPU() {
		t.Errorf("cpuCount = %d, want %d", rep.CPUCount, runtime.NumCPU())
	}
	// On Linux we expect real series; elsewhere telemetry is a documented no-op,
	// so only assert structural validity (non-negative, well-formed tuples).
	for _, series := range [][]report.TelemetryPoint{rep.CPUAvg, rep.CPUMax, rep.RAM} {
		for _, pt := range series {
			if pt[1] < 0 || pt[1] > 100 {
				t.Errorf("telemetry value %v out of range 0-100", pt[1])
			}
		}
	}
}

func TestCollector_LinuxProducesSamples(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("telemetry sampling is only implemented on linux")
	}
	c := NewCollector()
	// Burn a little CPU so there's measurable utilization between samples.
	for i := 0; i < 3; i++ {
		busy := 0
		for j := 0; j < 5_000_000; j++ {
			busy += j
		}
		_ = busy
		c.Sample(int64(1000 * (i + 1)))
	}
	var rep report.Report
	c.Enrich(&rep)
	if rep.RAMBytes <= 0 {
		t.Errorf("ramBytes = %d, want > 0 on linux", rep.RAMBytes)
	}
	if len(rep.RAM) == 0 {
		t.Errorf("expected RAM samples on linux")
	}
	if len(rep.CPUAvg) == 0 {
		t.Errorf("expected CPU samples on linux")
	}
}
