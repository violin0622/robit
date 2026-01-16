# Robit

A distributed mutex framework for Go with automatic lease renewal and loss notification.

## Features

- **Concurrent-safe**: All operations are protected by mutex
- **Non-reentrant**: Prevents accidental deadlocks from recursive locking
- **Driver-based**: Supports multiple backends via the `Driver` interface
- **Auto-renewal**: Automatically renews lease to prevent premature expiration
- **Loss notification**: Notifies when lock is lost due to TTL expiration

## Installation

```bash
go get github.com/violin0622/robit
```

## Quick Start

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/violin0622/robit"
    "github.com/violin0622/robit/local" // for testing; use redis/etcd in production
)

func main() {
    driver := local.New[string]()
    mutex := robit.New("order-lock", 30*time.Second, driver)

    ctx := context.Background()

    if err := mutex.LockCtx(ctx); err != nil {
        log.Fatal(err)
    }
    defer mutex.UnlockCtx(ctx)

    // Critical section - only one instance can execute this
    processOrder()
}
```

## Lock Loss Notification

When a lock is lost due to TTL expiration (network issues, slow renewal, etc.), you can be notified:

```go
ctx, cancel := context.WithCancel(context.Background())

mutex := robit.New("critical-task", 30*time.Second, driver,
    robit.WithOnLost[string](func() {
        log.Println("Lock lost! Stopping work...")
        cancel() // Cancel ongoing operations
    }),
)

if err := mutex.LockCtx(ctx); err != nil {
    log.Fatal(err)
}
defer mutex.UnlockCtx(context.Background())

// Do work, checking ctx.Done() periodically
for {
    select {
    case <-ctx.Done():
        return // Lock was lost
    default:
        doSomeWork()
    }
}
```

> **Note**: `onLost` is only called when the lock expires unexpectedly. It is NOT called when you voluntarily call `UnlockCtx()`.

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithRenewPeriod` | `ttl / 3` | How often to renew the lease |
| `WithRenewTimeout` | `ttl / 3` | Timeout for each renewal attempt |
| `WithOnLost` | none | Callbacks when lock is lost |
| `WithLogr` | discard | Logger for renewal errors |

```go
mutex := robit.New("my-lock", 30*time.Second, driver,
    robit.WithRenewPeriod[string](5*time.Second),
    robit.WithRenewTimeout[string](3*time.Second),
    robit.WithOnLost[string](onLostHandler),
    robit.WithLogr[string](logger),
)
```

## Built-in Drivers

### Local Driver (Testing Only)

```go
import "github.com/violin0622/robit/local"

driver := local.New[string]()
```

> ⚠️ The local driver is for testing only. Use Redis/etcd drivers in production.

## Implementing a Driver

To support a new backend (Redis, etcd, Consul, etc.), implement these interfaces:

```go
type Driver[ID any] interface {
    // Acquire attempts to acquire a lock with the given ID and TTL.
    // Returns a Lease on success, or an error if the lock is held.
    Acquire(ctx context.Context, id ID, ttl time.Duration) (Lease, error)

    // Release releases the given lease.
    Release(l Lease)
}

type Lease interface {
    // Refresh extends the lease TTL.
    Refresh(ctx context.Context, ttl time.Duration) error

    // Lost returns a channel that is closed when the lease expires.
    Lost() <-chan struct{}
}
```

### Example: Redis Driver

```go
type RedisDriver struct {
    client *redis.Client
}

func (d *RedisDriver) Acquire(ctx context.Context, id string, ttl time.Duration) (robit.Lease, error) {
    ok, err := d.client.SetNX(ctx, id, "locked", ttl).Result()
    if err != nil {
        return nil, err
    }
    if !ok {
        return nil, ErrLockHeld
    }
    return &RedisLease{client: d.client, key: id, ttl: ttl}, nil
}

func (d *RedisDriver) Release(l robit.Lease) {
    if rl, ok := l.(*RedisLease); ok {
        d.client.Del(context.Background(), rl.key)
    }
}
```

## Error Handling

| Error | Description |
|-------|-------------|
| `ErrAlreadyHeld` | Lock is already held (non-reentrant) |
| `ErrAlreadyReleased` | Unlock called on already released lock |

```go
err := mutex.LockCtx(ctx)
if errors.Is(err, robit.ErrAlreadyHeld) {
    // Handle reentrant lock attempt
}
```

## Statistics

Robit provides vendor-agnostic statistics via the `Stats()` method. Export to any monitoring system:

```go
mutex := robit.New("my-lock", 30*time.Second, driver)

// Get statistics snapshot
stats := mutex.Stats()

// Export to Prometheus
lockAcquiredCounter.Add(float64(stats.LockAcquired))
lockLostCounter.Add(float64(stats.LockLost))
holdTimeHistogram.Observe(stats.TotalHoldTime.Seconds())
```

### Available Metrics

| Metric | Description |
|--------|-------------|
| `LockAcquired` | Number of successful lock acquisitions |
| `LockFailed` | Number of failed lock attempts |
| `LockReleased` | Number of voluntary unlocks |
| `LockLost` | Number of locks lost due to TTL expiration |
| `RenewSuccess` | Number of successful lease renewals |
| `RenewFailed` | Number of failed lease renewals |
| `TotalHoldTime` | Cumulative lock hold duration |

## Thread Safety

- `LockCtx` and `UnlockCtx` are safe to call from multiple goroutines
- Only one `LockCtx` will succeed; others return `ErrAlreadyHeld`
- Multiple `UnlockCtx` calls are safe; only first succeeds
- `Stats()` returns a thread-safe snapshot

## License

MIT
