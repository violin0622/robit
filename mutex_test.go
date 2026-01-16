package robit_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/violin0622/robit"
)

// mockLease implements robit.Lease for testing
type mockLease struct {
	mu         sync.Mutex
	lost       chan struct{}
	closed     bool
	refreshErr error
	refreshCnt atomic.Int32
}

func newMockLease() *mockLease {
	return &mockLease{
		lost: make(chan struct{}),
	}
}

func (l *mockLease) Refresh(ctx context.Context, ttl time.Duration) error {
	l.refreshCnt.Add(1)
	if l.refreshErr != nil {
		return l.refreshErr
	}
	return nil
}

func (l *mockLease) Lost() <-chan struct{} {
	return l.lost
}

func (l *mockLease) triggerLost() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.lost)
	}
}

// mockDriver implements robit.Driver for testing
type mockDriver struct {
	mu         sync.Mutex
	lease      *mockLease
	acquireErr error
	acquireCnt atomic.Int32
	releaseCnt atomic.Int32
}

func newMockDriver() *mockDriver {
	return &mockDriver{}
}

func (d *mockDriver) Acquire(ctx context.Context, id string, ttl time.Duration) (robit.Lease, error) {
	d.acquireCnt.Add(1)
	if d.acquireErr != nil {
		return nil, d.acquireErr
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lease = newMockLease()
	return d.lease, nil
}

func (d *mockDriver) Release(l robit.Lease) {
	d.releaseCnt.Add(1)
}

func (d *mockDriver) getLease() *mockLease {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lease
}

// =============================================================================
// Basic Tests
// =============================================================================

func TestMutex_LockUnlock(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	// Lock should succeed
	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	// Unlock should succeed
	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("UnlockCtx failed: %v", err)
	}

	// Verify driver calls
	if d.acquireCnt.Load() != 1 {
		t.Errorf("expected 1 acquire, got %d", d.acquireCnt.Load())
	}
	if d.releaseCnt.Load() != 1 {
		t.Errorf("expected 1 release, got %d", d.releaseCnt.Load())
	}
}

func TestMutex_NonReentrant(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("first LockCtx failed: %v", err)
	}

	// Second lock should fail with ErrAlreadyHeld
	err := m.LockCtx(ctx)
	if !errors.Is(err, robit.ErrAlreadyHeld) {
		t.Errorf("expected ErrAlreadyHeld, got: %v", err)
	}

	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("UnlockCtx failed: %v", err)
	}
}

func TestMutex_DoubleUnlock(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("first UnlockCtx failed: %v", err)
	}

	// Second unlock should fail with ErrAlreadyReleased
	err := m.UnlockCtx(ctx)
	if !errors.Is(err, robit.ErrAlreadyReleased) {
		t.Errorf("expected ErrAlreadyReleased, got: %v", err)
	}
}

func TestMutex_UnlockWithoutLock(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	err := m.UnlockCtx(ctx)
	if !errors.Is(err, robit.ErrAlreadyReleased) {
		t.Errorf("expected ErrAlreadyReleased, got: %v", err)
	}
}

func TestMutex_AcquireError(t *testing.T) {
	d := newMockDriver()
	d.acquireErr = errors.New("acquire failed")
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	err := m.LockCtx(ctx)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestMutex_RelockAfterUnlock(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	// First lock/unlock cycle
	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("first LockCtx failed: %v", err)
	}
	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("first UnlockCtx failed: %v", err)
	}

	// Second lock/unlock cycle should work
	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("second LockCtx failed: %v", err)
	}
	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("second UnlockCtx failed: %v", err)
	}

	if d.acquireCnt.Load() != 2 {
		t.Errorf("expected 2 acquires, got %d", d.acquireCnt.Load())
	}
}

// =============================================================================
// OnLost Callback Tests
// =============================================================================

func TestMutex_OnLostCalledWhenLeaseLost(t *testing.T) {
	d := newMockDriver()
	lostCalled := make(chan struct{})

	m := robit.New("test", 1*time.Second, d,
		robit.WithOnLost[string](func() {
			close(lostCalled)
		}),
	)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	// Trigger lease lost
	d.getLease().triggerLost()

	// Wait for onLost callback
	select {
	case <-lostCalled:
		// Expected
	case <-time.After(1 * time.Second):
		t.Fatal("onLost callback not called")
	}
}

func TestMutex_OnLostNotCalledOnVoluntaryUnlock(t *testing.T) {
	d := newMockDriver()
	lostCalled := atomic.Bool{}

	m := robit.New("test", 1*time.Second, d,
		robit.WithOnLost[string](func() {
			lostCalled.Store(true)
		}),
	)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("UnlockCtx failed: %v", err)
	}

	// Give some time for any async callbacks
	time.Sleep(50 * time.Millisecond)

	if lostCalled.Load() {
		t.Error("onLost should not be called on voluntary unlock")
	}
}

// =============================================================================
// Renewal Tests
// =============================================================================

func TestMutex_RenewalHappens(t *testing.T) {
	d := newMockDriver()
	ttl := 300 * time.Millisecond
	m := robit.New("test", ttl, d,
		robit.WithRenewPeriod[string](50*time.Millisecond),
	)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	// Wait for some renewals
	time.Sleep(200 * time.Millisecond)

	if err := m.UnlockCtx(ctx); err != nil {
		t.Fatalf("UnlockCtx failed: %v", err)
	}

	refreshCnt := d.getLease().refreshCnt.Load()
	if refreshCnt < 2 {
		t.Errorf("expected at least 2 refreshes, got %d", refreshCnt)
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestMutex_ConcurrentLockAttempts(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	const goroutines = 100
	var successCnt atomic.Int32
	var alreadyHeldCnt atomic.Int32

	var wg sync.WaitGroup
	start := make(chan struct{})

	for range goroutines {
		wg.Go(func() {
			<-start
			err := m.LockCtx(ctx)
			if err == nil {
				successCnt.Add(1)
			} else if errors.Is(err, robit.ErrAlreadyHeld) {
				alreadyHeldCnt.Add(1)
			}
		})
	}

	close(start)
	wg.Wait()

	// Exactly one should succeed
	if successCnt.Load() != 1 {
		t.Errorf("expected exactly 1 success, got %d", successCnt.Load())
	}
	if alreadyHeldCnt.Load() != goroutines-1 {
		t.Errorf("expected %d already held, got %d", goroutines-1, alreadyHeldCnt.Load())
	}
}

func TestMutex_ConcurrentUnlockAttempts(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	const goroutines = 100
	var successCnt atomic.Int32
	var alreadyReleasedCnt atomic.Int32

	var wg sync.WaitGroup
	start := make(chan struct{})

	for range goroutines {
		wg.Go(func() {
			<-start
			err := m.UnlockCtx(ctx)
			if err == nil {
				successCnt.Add(1)
			} else if errors.Is(err, robit.ErrAlreadyReleased) {
				alreadyReleasedCnt.Add(1)
			}
		})
	}

	close(start)
	wg.Wait()

	// Exactly one should succeed
	if successCnt.Load() != 1 {
		t.Errorf("expected exactly 1 success, got %d", successCnt.Load())
	}
	if alreadyReleasedCnt.Load() != goroutines-1 {
		t.Errorf("expected %d already released, got %d", goroutines-1, alreadyReleasedCnt.Load())
	}
}

func TestMutex_UnlockDuringLeaseLost(t *testing.T) {
	d := newMockDriver()
	lostCalled := atomic.Int32{}

	m := robit.New("test", 1*time.Second, d,
		robit.WithOnLost[string](func() {
			lostCalled.Add(1)
		}),
	)
	ctx := context.Background()

	const iterations = 100
	for i := range iterations {
		if err := m.LockCtx(ctx); err != nil {
			t.Fatalf("iteration %d: LockCtx failed: %v", i, err)
		}

		// Race: trigger lost and unlock simultaneously
		var wg sync.WaitGroup
		wg.Go(func() { d.getLease().triggerLost() })
		wg.Go(func() { m.UnlockCtx(ctx) })
		wg.Wait()

		// Allow cleanup
		time.Sleep(10 * time.Millisecond)
	}

	// onLost should be called at most once per iteration (0 or 1)
	// Total should be <= iterations
	if lostCalled.Load() > int32(iterations) {
		t.Errorf("onLost called too many times: %d > %d", lostCalled.Load(), iterations)
	}
}

func TestMutex_RapidLockUnlockCycles(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 100*time.Millisecond, d,
		robit.WithRenewPeriod[string](10*time.Millisecond),
	)
	ctx := context.Background()

	const cycles = 50
	for i := range cycles {
		if err := m.LockCtx(ctx); err != nil {
			t.Fatalf("cycle %d: LockCtx failed: %v", i, err)
		}

		// Small random delay
		time.Sleep(time.Duration(i%10) * time.Millisecond)

		if err := m.UnlockCtx(ctx); err != nil {
			t.Fatalf("cycle %d: UnlockCtx failed: %v", i, err)
		}
	}

	if d.acquireCnt.Load() != cycles {
		t.Errorf("expected %d acquires, got %d", cycles, d.acquireCnt.Load())
	}
	if d.releaseCnt.Load() != cycles {
		t.Errorf("expected %d releases, got %d", cycles, d.releaseCnt.Load())
	}
}

func TestMutex_LostDuringRenewal(t *testing.T) {
	d := newMockDriver()
	lostCalled := make(chan struct{})

	m := robit.New("test", 1*time.Second, d,
		robit.WithRenewPeriod[string](20*time.Millisecond),
		robit.WithOnLost[string](func() {
			close(lostCalled)
		}),
	)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	// Wait for renewal to start
	time.Sleep(30 * time.Millisecond)

	// Trigger lost during renewal
	d.getLease().triggerLost()

	select {
	case <-lostCalled:
		// Expected
	case <-time.After(1 * time.Second):
		t.Fatal("onLost callback not called")
	}
}

// =============================================================================
// Race Detector Tests (run with -race)
// =============================================================================

func TestMutex_RaceDetector_ConcurrentOperations(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 500*time.Millisecond, d,
		robit.WithRenewPeriod[string](50*time.Millisecond),
	)
	ctx := context.Background()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Goroutine 1: repeatedly lock/unlock
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				if m.LockCtx(ctx) == nil {
					time.Sleep(10 * time.Millisecond)
					m.UnlockCtx(ctx)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	})

	// Goroutine 2: trigger lost periodically
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				if lease := d.getLease(); lease != nil {
					lease.triggerLost()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	})

	// Run for a while
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestMutex_IdempotentReset(t *testing.T) {
	d := newMockDriver()
	m := robit.New("test", 1*time.Second, d)
	ctx := context.Background()

	if err := m.LockCtx(ctx); err != nil {
		t.Fatalf("LockCtx failed: %v", err)
	}

	// Trigger lost and unlock at the same time to test idempotent reset
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			d.getLease().triggerLost()
		})
	}

	wg.Go(func() {
		m.UnlockCtx(ctx)
	})

	wg.Wait()

	// Should not panic, release should be called exactly once
	if d.releaseCnt.Load() != 1 {
		t.Errorf("expected exactly 1 release, got %d", d.releaseCnt.Load())
	}
}
