package robit

import (
	"sync/atomic"
	"time"
)

// Stats is a read-only snapshot of mutex statistics.
// Use Mutex.Stats() to obtain a snapshot that can be exported
// to any monitoring system (Prometheus, OpenTelemetry, StatsD, etc.).
type Stats struct {
	LockAcquired      int64         // Number of successful lock acquisitions
	LockFailed        int64         // Number of failed lock attempts
	LockReleased      int64         // Number of voluntary unlocks
	LockLost          int64         // Number of locks lost due to TTL expiration
	RenewSuccess      int64         // Number of successful lease renewals
	RenewFailed       int64         // Number of failed lease renewals
	TotalHoldDuration time.Duration // Cumulative lock hold duration
	HoldSince         time.Time     // Zero if not holding lock.
}

// stats uses atomic counters for thread-safe statistics collection.
type stats struct {
	lockacquired      atomic.Int64
	acquiredat        atomic.Int64 // nanoseconds timestamp
	lockfailed        atomic.Int64
	lockreleased      atomic.Int64
	locklost          atomic.Int64
	renewsuccess      atomic.Int64
	renewfailed       atomic.Int64
	totalHoldDuration atomic.Int64 // stored as nanoseconds
}

// snapshot returns a read-only copy of current statistics.
func (c *stats) snapshot() Stats {
	s := Stats{
		LockAcquired:      c.lockacquired.Load(),
		LockFailed:        c.lockfailed.Load(),
		LockReleased:      c.lockreleased.Load(),
		LockLost:          c.locklost.Load(),
		RenewSuccess:      c.renewsuccess.Load(),
		RenewFailed:       c.renewfailed.Load(),
		HoldSince:         nano2time(c.acquiredat.Load()),
		TotalHoldDuration: time.Duration(c.totalHoldDuration.Load()),
	}
	if at := c.acquiredat.Load(); at != 0 {
		s.TotalHoldDuration += time.Since(nano2time(at))
	}

	return s
}

func (c *stats) acquired() {
	c.lockacquired.Add(1)
	c.acquiredat.Store(time.Now().UnixNano())
}
func (c *stats) released() {
	c.lockreleased.Add(1)
	at := c.acquiredat.Swap(0)
	c.totalHoldDuration.Add(int64(time.Since(nano2time(at))))
}
func (c *stats) lost() {
	c.locklost.Add(1)
	at := c.acquiredat.Swap(0)
	c.totalHoldDuration.Add(int64(time.Since(nano2time(at))))
}
func (c *stats) failed()       { c.lockfailed.Add(1) }
func (c *stats) renewSuccess() { c.renewsuccess.Add(1) }
func (c *stats) renewFailed()  { c.renewfailed.Add(1) }

func nano2time(at int64) time.Time {
	if at == 0 {
		return time.Time{}
	}
	return time.Unix(at/1e9, at%1e9)
}
