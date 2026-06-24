package telemetry

import (
	"testing"
	"time"

	"github.com/mxschmitt/flakiness-go/report"
)

func TestSampler_StartStopEnriches(t *testing.T) {
	s := NewSampler()
	s.interval = 5 * time.Millisecond // sample quickly for the test
	s.Start()
	time.Sleep(30 * time.Millisecond)
	var rep report.Report
	s.Stop(&rep, time.Now().UnixMilli())

	// CPUCount is always set; series presence depends on platform support.
	if rep.CPUCount <= 0 {
		t.Errorf("cpuCount = %d, want > 0", rep.CPUCount)
	}
}

func TestSampler_StopWithoutPanic(t *testing.T) {
	// A sampler that's stopped almost immediately must not panic or hang.
	s := NewSampler()
	s.Start()
	var rep report.Report
	done := make(chan struct{})
	go func() {
		s.Stop(&rep, time.Now().UnixMilli())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Sampler.Stop hung")
	}
}
