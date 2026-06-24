package telemetry

import (
	"time"

	"github.com/mxschmitt/flakiness-go/report"
)

// Sampler drives a Collector on a fixed interval in the background. It is the
// runtime counterpart used by the runner: Start primes a baseline and begins a
// 1s ticker; Stop halts sampling, takes one final sample, and enriches the
// report.
type Sampler struct {
	c        *Collector
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
}

// NewSampler creates a Sampler with the default 1s interval.
func NewSampler() *Sampler {
	return &Sampler{
		c:        NewCollector(),
		interval: time.Second,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start begins background sampling. Safe to call once.
func (s *Sampler) Start() {
	go func() {
		defer close(s.done)
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case now := <-t.C:
				s.c.Sample(now.UnixMilli())
			}
		}
	}()
}

// Stop halts sampling, records a final sample, and writes the telemetry onto
// the report. Safe to call once after Start.
func (s *Sampler) Stop(rep *report.Report, nowMS int64) {
	close(s.stop)
	<-s.done
	s.c.Sample(nowMS)
	s.c.Enrich(rep)
}
