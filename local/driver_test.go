package local_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/violin0622/robit"
	"github.com/violin0622/robit/local"
)

func TestDriver_Acquire(t *testing.T) {
	d := local.New[string]()
	ctx := context.Background()

	// First acquire should succeed
	lease, err := d.Acquire(ctx, "test-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if lease == nil {
		t.Fatal("lease should not be nil")
	}

	// Second acquire with same ID should fail
	_, err = d.Acquire(ctx, "test-lock", 5*time.Second)
	if err != local.ErrLockHeld {
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

	// Release and acquire again
	d.Release(lease)

	lease3, err := d.Acquire(ctx, "test-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	if lease3 == nil {
		t.Fatal("lease3 should not be nil")
	}
}

func TestLease_Refresh(t *testing.T) {
	d := local.New[string]()
	ctx := context.Background()

	lease, err := d.Acquire(ctx, "test-lock", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// Refresh before expiry
	time.Sleep(50 * time.Millisecond)
	if err := lease.Refresh(ctx, 100*time.Millisecond); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	// Should still be valid
	select {
	case <-lease.Lost():
		t.Fatal("lease should not be lost yet")
	default:
	}
}

func TestLease_Lost(t *testing.T) {
	d := local.New[string]()
	ctx := context.Background()

	lease, err := d.Acquire(ctx, "test-lock", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// Wait for expiry
	select {
	case <-lease.Lost():
		// Expected
	case <-time.After(200 * time.Millisecond):
		t.Fatal("lease should have been lost")
	}

	// Refresh after expiry should fail
	if err := lease.Refresh(ctx, 100*time.Millisecond); err == nil {
		t.Fatal("refresh after expiry should fail")
	}

	// Should be able to acquire again after expiry
	_, err = d.Acquire(ctx, "test-lock", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire after expiry failed: %v", err)
	}
}

func TestWithRobitMutex(t *testing.T) {
	d := local.New[string]()

	m := robit.New(
		"test-lock",
		100*time.Millisecond,
		d)

	ctx := context.Background()

	// Lock
	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("lock failed: %v", err)
	}

	fmt.Println(`0`)
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
