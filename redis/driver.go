// Package redis provides a Redis-based implementation of robit.Driver
// for distributed locking using Redis SET NX with TTL.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/violin0622/robit"
)

var (
	// ErrLockLost   = errors.New("lock has been lost")
	ErrNotOurLock = errors.New("lock is not owned by this lease")
)

// Lua script to release lock only if we own it
var releaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`)

// Lua script to refresh lock only if we own it
var refreshScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("pexpire", KEYS[1], ARGV[2])
else
    return 0
end
`)

// Driver implements robit.Driver[string] using Redis.
type Driver struct {
	client redis.Cmdable
	prefix string
	token  string // custom token for lock ownership, if empty a random one is generated
}

// Option configures a Driver.
type Option func(*Driver)

// WithPrefix sets a key prefix for all locks.
func WithPrefix(prefix string) Option {
	return func(d *Driver) {
		d.prefix = prefix
	}
}

// WithToken sets a custom token to identify lock ownership.
// If not set, a random token will be generated for each lock acquisition.
// This is useful when you want to identify the lock holder (e.g., by hostname or process ID).
func WithToken(token string) Option {
	return func(d *Driver) {
		d.token = token
	}
}

// New creates a new Redis Driver instance.
func New(client redis.Cmdable, opts ...Option) *Driver {
	d := &Driver{
		client: client,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Acquire attempts to acquire a lock with the given ID and TTL.
// Returns ErrLockHeld if the lock is already held by another process.
func (d *Driver) Acquire(ctx context.Context, id string, ttl time.Duration) (robit.Lease, error) {
	key := d.prefix + id
	token := d.token
	if token == "" {
		token = generateToken()
	}

	// Try to acquire the lock using SET NX PX
	ok, err := d.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, robit.ErrAlreadyHeld
	}

	// Create lease with background monitoring
	l := newLease(d.client, key, token, ttl)
	return l, nil
}

// Release releases the given lease.
func (d *Driver) Release(l robit.Lease) {
	if l == nil {
		return
	}

	ll, ok := l.(*Lease)
	if !ok {
		return
	}

	ll.release()
}

// Lease implements robit.Lease for Redis driver.
type Lease struct {
	client redis.Cmdable
	key    string
	token  string

	mu       sync.Mutex
	lost     chan struct{}
	closed   bool
	stopCh   chan struct{}
	checkTTL time.Duration
}

func newLease(client redis.Cmdable, key, token string, ttl time.Duration) *Lease {
	l := &Lease{
		client:   client,
		key:      key,
		token:    token,
		lost:     make(chan struct{}),
		stopCh:   make(chan struct{}),
		checkTTL: ttl,
	}

	// Start background goroutine to monitor lock status
	go l.monitor()

	return l
}

// Refresh extends the lease TTL.
func (l *Lease) Refresh(ctx context.Context, ttl time.Duration) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return robit.ErrAlreadyReleased
	}
	l.checkTTL = ttl
	l.mu.Unlock()

	// Use Lua script to atomically check and refresh
	result, err := refreshScript.Run(ctx, l.client, []string{l.key}, l.token, ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		l.markLost()
		return ErrNotOurLock
	}

	return nil
}

// Lost returns a channel that is closed when the lease is lost.
func (l *Lease) Lost() <-chan struct{} {
	return l.lost
}

// monitor periodically checks if the lock is still held.
func (l *Lease) monitor() {
	// Check at half the TTL interval to detect loss before expiration
	ticker := time.NewTicker(l.checkTTL / 2)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.mu.Lock()
			if l.closed {
				l.mu.Unlock()
				return
			}
			checkTTL := l.checkTTL
			l.mu.Unlock()

			// Update ticker interval if TTL changed
			ticker.Reset(checkTTL / 2)

			// Check if we still own the lock
			ctx, cancel := context.WithTimeout(context.Background(), checkTTL/4)
			val, err := l.client.Get(ctx, l.key).Result()
			cancel()

			if err == redis.Nil || (err == nil && val != l.token) {
				// Lock is gone or owned by someone else
				l.markLost()
				return
			}
			// If there's a network error, we continue monitoring
			// The lock might still be valid
		}
	}
}

func (l *Lease) markLost() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	l.closed = true
	close(l.lost)
}

func (l *Lease) release() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	close(l.lost)
	close(l.stopCh)
	l.mu.Unlock()

	// Release the lock in Redis using Lua script
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	releaseScript.Run(ctx, l.client, []string{l.key}, l.token)
}

// generateToken creates a unique token for lock ownership.
func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
