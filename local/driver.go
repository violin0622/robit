// Package local provides a local in-memory implementation of robit.Driver
// based on sync.Mutex. This is intended for testing and demonstration purposes only.
// It should NOT be used in production distributed systems.
package local

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/violin0622/robit"
)

var ErrLockHeld = errors.New("lock is already held")

// Driver implements robit.Driver using local sync.Mutex.
// This is a simple implementation for testing purposes.
type Driver[ID comparable] struct {
	mu    sync.Mutex
	locks map[ID]*Lease
}

// New creates a new local Driver instance.
func New[ID comparable]() *Driver[ID] {
	return &Driver[ID]{
		locks: make(map[ID]*Lease),
	}
}

// Acquire attempts to acquire a lock with the given ID and TTL.
// Returns ErrLockHeld if the lock is already held by another lease.
func (d *Driver[ID]) Acquire(ctx context.Context, id ID, ttl time.Duration) (robit.Lease, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if holder, exists := d.locks[id]; exists && !holder.isExpired() {
		return nil, ErrLockHeld
	}

	// Create new lease
	var l *Lease
	l = newLease(ttl, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.locks[id] == l {
			delete(d.locks, id)
		}
	})

	d.locks[id] = l
	return l, nil
}

// Release releases the given lease.
func (d *Driver[ID]) Release(l robit.Lease) {
	if l == nil {
		return
	}

	ll, ok := l.(*Lease)
	if !ok {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	ll.release()

	// Remove from locks map
	for id, holder := range d.locks {
		if holder == ll {
			delete(d.locks, id)
			break
		}
	}
}

// Lease implements robit.Lease for local driver.
type Lease struct {
	mu       sync.Mutex
	timer    *time.Timer
	lost     chan struct{}
	closed   bool
	onExpire func()
}

func newLease(ttl time.Duration, onExpire func()) *Lease {
	l := &Lease{
		lost:     make(chan struct{}),
		onExpire: onExpire,
	}
	l.resetTTL(ttl)
	return l
}

// Refresh extends the lease TTL.
func (l *Lease) Refresh(ctx context.Context, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return errors.New("lease expired")
	}

	l.resetTTL(ttl)
	return nil
}

// Lost returns a channel that is closed when the lease expires.
func (l *Lease) Lost() <-chan struct{} {
	return l.lost
}

func (l *Lease) isExpired() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func (l *Lease) resetTTL(ttl time.Duration) {
	if l.timer != nil {
		l.timer.Stop()
	}

	l.timer = time.AfterFunc(ttl, func() {
		l.mu.Lock()
		defer l.mu.Unlock()

		if l.closed {
			return
		}

		l.closed = true
		close(l.lost)

		if l.onExpire == nil {
			return
		}
		// Run in goroutine to avoid holding lease lock
		go l.onExpire()
	})
}

func (l *Lease) release() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}

	if !l.closed {
		l.closed = true
		close(l.lost)
	}
}
