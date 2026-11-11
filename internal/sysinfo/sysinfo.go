// Package sysinfo reports host-level CPU and memory usage for the console's
// "node resources" panel.
//
// It deliberately reports the *host* rather than the Go process: an operator
// watching the dashboard wants to know whether the machine is running out of
// memory, not how much the runtime has reserved. Process-level numbers
// (goroutines, heap) stay in the server package where they belong.
package sysinfo

import (
	"sync"
	"time"
)

// Stats is a point-in-time view of the host.
type Stats struct {
	// CPUPercent is the share of total CPU time that was busy across the last
	// sampling window, 0-100. Stale when Available is false.
	CPUPercent float64
	// MemTotal and MemUsed are host RAM in bytes.
	MemTotal uint64
	MemUsed  uint64
	// Available is false when the platform is unsupported or the kernel data
	// could not be read; callers should hide the widget rather than show 0%.
	Available bool
}

// cpuSample is cumulative CPU time; only deltas between two reads are useful.
type cpuSample struct {
	busy  uint64
	total uint64
}

// Sampler polls the host on a fixed interval so CPU percentage can be derived
// from the delta between two reads. The first read only establishes a
// baseline, so a freshly started process reports 0% until the next tick.
type Sampler struct {
	mu       sync.RWMutex
	stats    Stats
	interval time.Duration
	stop     chan struct{}
	stopOnce sync.Once

	prev     cpuSample
	havePrev bool
}

// NewSampler builds a sampler. A zero or negative interval defaults to 2s.
func NewSampler(interval time.Duration) *Sampler {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Sampler{
		interval: interval,
		stats:    Stats{Available: true},
		stop:     make(chan struct{}),
	}
}

// Start samples once synchronously (cheap, and makes the first response
// meaningful sooner) then keeps polling in the background until Stop.
func (s *Sampler) Start() {
	s.sample()
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.sample()
			}
		}
	}()
}

// Stop releases the background goroutine. Safe to call more than once.
func (s *Sampler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// Snapshot returns the most recent reading. The value is a copy, so callers may
// hold on to it without locking.
func (s *Sampler) Snapshot() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

func (s *Sampler) sample() {
	memTotal, memUsed, memOK := readMem()
	cur, cpuOK := readCPU()

	s.mu.Lock()
	defer s.mu.Unlock()

	stats := s.stats // start from the previous reading so an unreadable field keeps its last value
	stats.Available = false
	if memOK {
		stats.MemTotal, stats.MemUsed = memTotal, memUsed
		stats.Available = true
	}
	if cpuOK {
		if s.havePrev && cur.total > s.prev.total && cur.busy >= s.prev.busy {
			// Guard against counters going backwards (suspend/resume, counter
			// wrap) instead of emitting a nonsense percentage.
			deltaTotal := cur.total - s.prev.total
			deltaBusy := cur.busy - s.prev.busy
			pct := float64(deltaBusy) / float64(deltaTotal) * 100
			if pct > 100 {
				pct = 100
			}
			stats.CPUPercent = pct
		}
		s.prev, s.havePrev = cur, true
		stats.Available = true
	}
	s.stats = stats
}
