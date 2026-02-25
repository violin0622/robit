package redis_test

import (
	"context"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/violin0622/robit"
	"github.com/violin0622/robit/redis"
)

func getRedisClient(t *testing.T) *goredis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client := goredis.NewClient(&goredis.Options{
		Addr: addr,
	})

	// Check connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available at %s: %v", addr, err)
	}

	return client
}

func TestDriver_Acquire(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	d := redis.New(client, redis.WithPrefix("robit:test:"))
	ctx := context.Background()

	// Clean up before test
	client.Del(ctx, "robit:test:test-lock", "robit:test:other-lock")

	// First acquire should succeed
	lease, err := d.Acquire(ctx, "test-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if lease == nil {
		t.Fatal("lease should not be nil")
	}
	defer d.Release(lease)

	// Second acquire with same ID should fail
	_, err = d.Acquire(ctx, "test-lock", 5*time.Second)
	if err != robit.ErrAlreadyHeld {
		t.Fatalf("expected ErrLockHeld, got: %v", err)
	}

	// Acquire with different ID should succeed
	lease2, err := d.Acquire(ctx, "other-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire different lock failed: %v", err)
	}
	if lease2 == nil {
		t.Fatal("lease2 should not be nil")
	}
	defer d.Release(lease2)

	// Release and acquire again
	d.Release(lease)

	lease3, err := d.Acquire(ctx, "test-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	if lease3 == nil {
		t.Fatal("lease3 should not be nil")
	}
	defer d.Release(lease3)
}

func TestLease_Refresh(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	d := redis.New(client, redis.WithPrefix("robit:test:"))
	ctx := context.Background()

	// Clean up before test
	client.Del(ctx, "robit:test:refresh-lock")

	lease, err := d.Acquire(ctx, "refresh-lock", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer d.Release(lease)

	// Refresh before expiry
	time.Sleep(200 * time.Millisecond)
	if err := lease.Refresh(ctx, 500*time.Millisecond); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	// Should still be valid
	select {
	case <-lease.Lost():
		t.Fatal("lease should not be lost yet")
	default:
	}

	// Verify TTL was extended in Redis
	ttl, err := client.PTTL(ctx, "robit:test:refresh-lock").Result()
	if err != nil {
		t.Fatalf("failed to get TTL: %v", err)
	}
	if ttl < 400*time.Millisecond {
		t.Fatalf("TTL should be close to 500ms, got: %v", ttl)
	}
}

func TestLease_Lost(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	d := redis.New(client, redis.WithPrefix("robit:test:"))
	ctx := context.Background()

	// Clean up before test
	client.Del(ctx, "robit:test:lost-lock")

	lease, err := d.Acquire(ctx, "lost-lock", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// Wait for expiry
	select {
	case <-lease.Lost():
		// Expected - lock expired and monitor detected it
	case <-time.After(1 * time.Second):
		t.Fatal("lease should have been lost")
	}

	// Refresh after expiry should fail
	if err := lease.Refresh(ctx, 100*time.Millisecond); err == nil {
		t.Fatal("refresh after expiry should fail")
	}

	// Should be able to acquire again after expiry
	lease2, err := d.Acquire(ctx, "lost-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire after expiry failed: %v", err)
	}
	d.Release(lease2)
}

func TestLease_LostByExternalDeletion(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	d := redis.New(client, redis.WithPrefix("robit:test:"))
	ctx := context.Background()

	// Clean up before test
	client.Del(ctx, "robit:test:external-lock")

	lease, err := d.Acquire(ctx, "external-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// Simulate external deletion (e.g., by another process or admin)
	client.Del(ctx, "robit:test:external-lock")

	// Wait for monitor to detect loss
	select {
	case <-lease.Lost():
		// Expected
	case <-time.After(3 * time.Second):
		t.Fatal("lease should have been lost after external deletion")
	}
}

func TestWithRobitMutex(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	d := redis.New(client, redis.WithPrefix("robit:test:"))
	ctx := context.Background()

	// Clean up before test
	client.Del(ctx, "robit:test:mutex-lock")

	m := robit.New(
		"mutex-lock",
		500*time.Millisecond,
		d)

	// Lock
	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("lock failed: %v", err)
	}

	// Unlock
	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}

	// Lock again should work
	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("second lock failed: %v", err)
	}

	// Double lock should fail
	if err := m.LockCtx(ctx); err != robit.ErrAlreadyHeld {
		t.Fatalf("expected ErrAlreadyHeld, got: %v", err)
	}

	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("final unlock failed: %v", err)
	}
}

func TestDriver_Release_Nil(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	d := redis.New(client)

	// Should not panic
	d.Release(nil)
}

func TestDriver_WithPrefix(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	d := redis.New(client, redis.WithPrefix("myapp:locks:"))
	ctx := context.Background()

	// Clean up before test
	client.Del(ctx, "myapp:locks:prefixed-lock")

	lease, err := d.Acquire(ctx, "prefixed-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer d.Release(lease)

	// Verify the key has the prefix
	exists, err := client.Exists(ctx, "myapp:locks:prefixed-lock").Result()
	if err != nil {
		t.Fatalf("exists check failed: %v", err)
	}
	if exists != 1 {
		t.Fatal("key with prefix should exist")
	}
}
