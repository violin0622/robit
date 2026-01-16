// Package robit provides a distributed mutex implementation with automatic lease renewal.
//
// # Overview
//
// Robit is a framework for building distributed locks with the following features:
//   - Concurrent-safe: All operations are protected by mutex
//   - Non-reentrant: Prevents accidental deadlocks from recursive locking
//   - Driver-based: Supports multiple backends (Redis, etcd, etc.) via the Driver interface
//   - Auto-renewal: Automatically renews lease to prevent premature expiration
//   - Loss notification: Notifies when lock is lost due to TTL expiration
//
// # Basic Usage
//
//	driver := redis.NewDriver(redisClient) // your driver implementation
//	mutex := robit.New("my-lock", 30*time.Second, driver)
//
//	ctx := context.Background()
//	if err := mutex.LockCtx(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer mutex.UnlockCtx(ctx)
//
//	// critical section
//
// # Lock Loss Notification
//
// When a lock is lost due to TTL expiration (e.g., network partition, slow renewal),
// the onLost callbacks are invoked:
//
//	mutex := robit.New("my-lock", 30*time.Second, driver,
//	    robit.WithOnLost[string](func() {
//	        log.Println("Lock lost! Stopping critical operations...")
//	        cancel() // cancel ongoing work
//	    }),
//	)
//
// Note: onLost is NOT called when you voluntarily call UnlockCtx.
//
// # Statistics
//
// Robit provides vendor-agnostic statistics via the Stats() method:
//
//	stats := mutex.Stats()
//	fmt.Printf("Acquired: %d, Lost: %d\n", stats.LockAcquired, stats.LockLost)
//
// The Stats struct contains: LockAcquired, LockFailed, LockReleased, LockLost,
// RenewSuccess, RenewFailed, and TotalHoldTime. Export to any monitoring system
// (Prometheus, OpenTelemetry, StatsD, etc.).
//
// # Implementing a Driver
//
// To support a new backend, implement the Driver and Lease interfaces:
//
//	type Driver[ID any] interface {
//	    Acquire(context.Context, ID, time.Duration) (Lease, error)
//	    Release(Lease)
//	}
//
//	type Lease interface {
//	    Refresh(context.Context, time.Duration) error
//	    Lost() <-chan struct{}
//	}
//
// The Lost() channel should be closed when the lease expires or is revoked.
package robit
