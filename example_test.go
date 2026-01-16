package robit_test

import (
	"context"
	"fmt"
	"time"

	"github.com/violin0622/robit"
	"github.com/violin0622/robit/local"
)

func Example() {
	driver := local.New[string]()
	mutex := robit.New("order-process", 30*time.Second, driver)

	ctx := context.Background()

	// Acquire the lock
	if err := mutex.LockCtx(ctx); err != nil {
		fmt.Println("Failed to acquire lock:", err)
		return
	}

	// Critical section - only one instance can execute this
	fmt.Println("Processing order...")

	// Release the lock
	if err := mutex.UnlockCtx(ctx); err != nil {
		fmt.Println("Failed to release lock:", err)
		return
	}

	fmt.Println("Done")
	// Output:
	// Processing order...
	// Done
}

func Example_withOnLost() {
	driver := local.New[string]()

	// Create a cancellable context for the critical section
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mutex := robit.New("critical-task", 30*time.Second, driver,
		robit.WithOnLost[string](func() {
			fmt.Println("Lock lost! Cancelling operations...")
			cancel()
		}),
	)

	if err := mutex.LockCtx(ctx); err != nil {
		fmt.Println("Failed to acquire lock:", err)
		return
	}
	defer mutex.UnlockCtx(context.Background())

	// Simulate critical work
	fmt.Println("Working...")

	// Output:
	// Working...
}

func Example_nonReentrant() {
	driver := local.New[string]()
	mutex := robit.New("my-lock", 30*time.Second, driver)

	ctx := context.Background()

	// First lock succeeds
	if err := mutex.LockCtx(ctx); err != nil {
		fmt.Println("First lock failed:", err)
		return
	}

	// Second lock fails - mutex is non-reentrant
	if err := mutex.LockCtx(ctx); err != nil {
		fmt.Println("Second lock:", err)
	}

	mutex.UnlockCtx(ctx)
	// Output:
	// Second lock: already held lock
}

func Example_customRenewal() {
	driver := local.New[string]()

	// Customize renewal settings
	mutex := robit.New("my-lock", 30*time.Second, driver,
		robit.WithRenewPeriod[string](5*time.Second),  // Renew every 5 seconds
		robit.WithRenewTimeout[string](3*time.Second), // Renewal timeout 3 seconds
	)

	ctx := context.Background()

	if err := mutex.LockCtx(ctx); err != nil {
		fmt.Println("Failed to acquire lock:", err)
		return
	}

	fmt.Println("Lock acquired with custom renewal settings")

	mutex.UnlockCtx(ctx)
	// Output:
	// Lock acquired with custom renewal settings
}

func Example_lockLost() {
	driver := local.New[string]()
	lostNotified := make(chan struct{})

	// Use short TTL to demonstrate lock loss
	mutex := robit.New("expiring-lock", 50*time.Millisecond, driver,
		robit.WithRenewPeriod[string](100*time.Millisecond), // Renew slower than TTL
		robit.WithOnLost[string](func() {
			fmt.Println("Lock was lost due to TTL expiration!")
			close(lostNotified)
		}),
	)

	ctx := context.Background()

	if err := mutex.LockCtx(ctx); err != nil {
		fmt.Println("Failed to acquire lock:", err)
		return
	}

	fmt.Println("Lock acquired, waiting for expiration...")

	// Wait for lock to expire (renewal is slower than TTL)
	<-lostNotified

	fmt.Println("Cleanup complete")
	// Output:
	// Lock acquired, waiting for expiration...
	// Lock was lost due to TTL expiration!
	// Cleanup complete
}

func Example_stats() {
	driver := local.New[string]()
	mutex := robit.New("my-lock", 30*time.Second, driver)

	ctx := context.Background()

	// Perform some lock operations
	mutex.LockCtx(ctx)
	mutex.UnlockCtx(ctx)

	mutex.LockCtx(ctx)
	mutex.UnlockCtx(ctx)

	// Try to lock while already locked (will fail)
	mutex.LockCtx(ctx)
	mutex.LockCtx(ctx) // This will fail
	mutex.UnlockCtx(ctx)

	// Get statistics snapshot
	stats := mutex.Stats()
	fmt.Printf("Acquired: %d\n", stats.LockAcquired)
	fmt.Printf("Released: %d\n", stats.LockReleased)
	fmt.Printf("Failed: %d\n", stats.LockFailed)

	// Output:
	// Acquired: 3
	// Released: 3
	// Failed: 1
}
