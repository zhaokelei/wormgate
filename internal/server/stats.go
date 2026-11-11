package server

import (
	"context"
	"sync"
	"time"
)

const (
	statsInterval = time.Second
	statsWindow   = 300 // five minutes of one-second samples
)

// RateSample is one in/out rate bucket. Timestamps are Unix milliseconds.
type RateSample struct {
	Timestamp int64  `json:"timestamp"`
	BytesIn   uint64 `json:"bytes_in"`
	BytesOut  uint64 `json:"bytes_out"`
}

// TunnelSeries is the time series for one live registration.
type TunnelSeries struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Owner      string       `json:"owner"`
	Type       string       `json:"type"`
	PublicAddr string       `json:"public_addr"`
	Samples    []RateSample `json:"samples"`
}

type prevCounters struct {
	in, out uint64
}

// Stats samples registrations once per second and retains a short ring for
// the dashboard charts. It never stores per-connection data.
type Stats struct {
	mu       sync.Mutex
	global   []RateSample
	series   map[string]*TunnelSeries
	previous map[string]prevCounters
	lastSeen map[string]time.Time
	lastGlob prevCounters
	started  time.Time
}

func newStats() *Stats {
	return &Stats{
		series:   map[string]*TunnelSeries{},
		previous: map[string]prevCounters{},
		lastSeen: map[string]time.Time{},
		started:  time.Now(),
	}
}

// Run samples until ctx ends.
func (s *Stats) Run(ctx context.Context, list func() []*Registration) {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.sample(now, list())
		}
	}
}

func (s *Stats) sample(now time.Time, regs []*Registration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ms := now.UnixMilli()
	var totalIn, totalOut uint64
	live := make(map[string]bool, len(regs))
	for _, reg := range regs {
		live[reg.ID] = true
		in := reg.Counters.BytesIn.Load()
		out := reg.Counters.BytesOut.Load()
		prev := s.previous[reg.ID]
		dIn, dOut := in-prev.in, out-prev.out
		if in < prev.in {
			dIn = in
		}
		if out < prev.out {
			dOut = out
		}
		s.previous[reg.ID] = prevCounters{in: in, out: out}
		s.lastSeen[reg.ID] = now
		totalIn += dIn
		totalOut += dOut

		series := s.series[reg.ID]
		if series == nil {
			series = &TunnelSeries{
				ID:         reg.ID,
				Name:       reg.Name,
				Owner:      reg.OwnerName,
				Type:       reg.Type,
				PublicAddr: reg.PublicAddr,
			}
			s.series[reg.ID] = series
		}
		appendSample(&series.Samples, RateSample{Timestamp: ms, BytesIn: dIn, BytesOut: dOut})
	}

	// Account for bytes of tunnels that disappeared between ticks.
	s.lastGlob = prevCounters{in: s.lastGlob.in + totalIn, out: s.lastGlob.out + totalOut}
	appendSample(&s.global, RateSample{Timestamp: ms, BytesIn: totalIn, BytesOut: totalOut})

	cutoff := now.Add(-2 * time.Minute)
	for id, seen := range s.lastSeen {
		if live[id] {
			continue
		}
		if seen.Before(cutoff) {
			delete(s.series, id)
			delete(s.previous, id)
			delete(s.lastSeen, id)
		}
	}
}

func appendSample(dst *[]RateSample, s RateSample) {
	if len(*dst) == statsWindow {
		copy(*dst, (*dst)[1:])
		(*dst)[len(*dst)-1] = s
		return
	}
	*dst = append(*dst, s)
}

// Snapshot returns a copy of the global series and all retained tunnel series.
func (s *Stats) Snapshot() (global []RateSample, tunnels []TunnelSeries) {
	s.mu.Lock()
	defer s.mu.Unlock()
	global = append([]RateSample(nil), s.global...)
	tunnels = make([]TunnelSeries, 0, len(s.series))
	for _, series := range s.series {
		cp := *series
		cp.Samples = append([]RateSample(nil), series.Samples...)
		tunnels = append(tunnels, cp)
	}
	return global, tunnels
}

// Uptime returns the server uptime.
func (s *Stats) Uptime() time.Duration { return time.Since(s.started) }

// AvgRates returns bytes/second over the last n samples.
func latestRates(samples []RateSample, n int) (in, out uint64) {
	if n <= 0 || n > len(samples) {
		n = len(samples)
	}
	if n == 0 {
		return 0, 0
	}
	for _, sample := range samples[len(samples)-n:] {
		in += sample.BytesIn
		out += sample.BytesOut
	}
	return in / uint64(n), out / uint64(n)
}
