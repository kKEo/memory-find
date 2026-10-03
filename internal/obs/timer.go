package obs

import "time"

// Timer measures one duration.
type Timer struct{ start time.Time }

// Start begins timing.
func Start() Timer { return Timer{start: time.Now()} }

// Elapsed is the time since Start.
func (t Timer) Elapsed() time.Duration { return time.Since(t.start) }

// Seconds is Elapsed in seconds.
func (t Timer) Seconds() float64 { return t.Elapsed().Seconds() }

// ObserveTo records Seconds into a histogram.
func (t Timer) ObserveTo(h *Histogram) { h.Observe(t.Seconds()) }
