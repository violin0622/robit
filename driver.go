package robit

import (
	"context"
	"errors"
	"time"
)

// Driver is the low-level interface for distributed lock backends, responsible
// for acquiring and releasing locks. ID is the lock identifier type used to
// distinguish different lock targets.
type Driver[ID comparable] interface {
	// Acquire attempts to obtain the lock identified by ID with the given TTL.
	// On success it returns a valid Lease; otherwise it returns an error.
	// It should return ErrAlreadyHeld if the caller already holds the lock, or
	// ErrAcquireFailed if the lock is held by another party.
	// When ctx is cancelled, Acquire should abort promptly and return ctx.Err().
	//
	// Concurrency: Acquire may be called concurrently from multiple goroutines
	// (e.g., multiple Mutex instances sharing the same Driver). Implementations
	// must be safe for concurrent use.
	Acquire(context.Context, ID, time.Duration) (Lease, error)

	// Release relinquishes the given Lease and frees the underlying lock resource.
	// Release is called exactly once for each successful Acquire, regardless of
	// whether the unlock is voluntary or caused by lease loss.
	// When called on an already-released Lease, it should be a no-op.
	//
	// Concurrency: Release is never called concurrently for the same Lease, but
	// may be called concurrently for different Leases (e.g., when multiple Mutex
	// instances share the same Driver). Implementations must be safe for
	// concurrent use across different Leases.
	Release(Lease)
}

// Lease represents an active hold on a lock obtained via a successful Acquire.
// A lease has a finite time-to-live and must be periodically refreshed to
// maintain ownership.
//
// Implementation constraints:
//   - The channel returned by Lost must be closed exactly once when the lease
//     is lost.
//   - A Refresh error does not imply immediate lease loss; only the closure of
//     the Lost channel signals that the lease is truly gone.
type Lease interface {
	// Refresh extends the lease's time-to-live to the specified duration.
	// It should return an error if the underlying store detects that the lease
	// has been taken over by another holder or has already expired.
	// A Refresh failure does not automatically trigger Lost; implementations
	// should rely on an independent monitor to determine actual lease loss.
	//
	// Concurrency: Refresh is always called sequentially from a single
	// goroutine (the keepalive loop). Implementations do not have to be safe for
	// concurrent use on the same Lease.
	Refresh(context.Context, time.Duration) error

	// Lost returns a channel that is closed when the lease is lost due to
	// expiration, revocation, or an underlying failure. Callers should select
	// on this channel to detect lock loss and stop protected critical-section
	// work accordingly. The channel is close-only — no values are ever sent.
	Lost() <-chan struct{}
}

var ErrAlreadyHeld = errors.New(`already held lock`)
var ErrAlreadyReleased = errors.New(`already released lock`)
var ErrAcquireFailed = errors.New(`lock is already been acquired by another instance`)
