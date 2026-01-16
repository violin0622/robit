package robit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

type Mutex[ID any] struct {
	id           ID
	ttl          time.Duration
	renewPeriod  time.Duration
	renewTimeout time.Duration
	onLost       []func()
	log          logr.Logger
	d            Driver[ID]
	s            stats

	wg     sync.WaitGroup
	mu     sync.Mutex
	cancel context.CancelCauseFunc
	l      Lease
	t      *time.Ticker
}

var ErrAlreadyHeld = errors.New(`already held lock`)
var ErrAlreadyReleased = errors.New(`already released lock`)

func New[ID any](id ID, ttl time.Duration, d Driver[ID], opts ...Option[ID]) *Mutex[ID] {

	m := &Mutex[ID]{
		id:           id,
		ttl:          ttl,
		renewPeriod:  ttl / 3,
		renewTimeout: ttl / 3,
		log:          logr.Discard(),
		d:            d,
	}

	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *Mutex[ID]) LockCtx(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.s.failed()
		return ErrAlreadyHeld
	}

	lease, err := m.d.Acquire(ctx, m.id, m.ttl)
	if err != nil {
		m.s.failed()
		return fmt.Errorf(`lock ctx: %w`, err)
	}

	m.l = lease
	ctx2, cancel := context.WithCancelCause(context.Background())
	m.cancel = cancel
	m.t = time.NewTicker(m.renewPeriod)
	m.wg.Go(func() { m.keepalive(ctx2, lease) })
	m.s.acquired()
	return nil
}

func (m *Mutex[ID]) UnlockCtx(ctx context.Context) error {
	if err := m.tryCancel(); err != nil {
		return err
	}

	m.wg.Wait()

	m.reset()
	return nil
}

func (m *Mutex[ID]) tryCancel() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel == nil {
		return ErrAlreadyReleased
	}

	m.cancel(fmt.Errorf(`unlock`))
	m.cancel = nil
	return nil
}

func (m *Mutex[ID]) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil { // Lock was lost (not voluntarily released)
		m.s.lost()
		m.lost()
	} else { // Voluntarily released
		m.s.released()
	}

	if m.l != nil {
		m.d.Release(m.l)
	}

	if m.t != nil {
		m.t.Stop()
	}

	m.l, m.t, m.cancel = nil, nil, nil
}

func (m *Mutex[ID]) lost() {
	for _, f := range m.onLost {
		f()
	}
}

// Stats returns a snapshot of the mutex statistics.
// The returned struct is a copy and safe to use without synchronization.
func (m *Mutex[ID]) Stats() Stats {
	return m.s.snapshot()
}

func (m *Mutex[ID]) keepalive(ctx context.Context, l Lease) {
NEXT:
	select {
	case <-ctx.Done():
		return
	case <-l.Lost():
		m.reset()
		return
	case <-m.t.C:
		m.renewLease(l)
		goto NEXT
	}
}

func (m *Mutex[ID]) renewLease(l Lease) {
	ctx, cancel := context.WithTimeoutCause(
		context.Background(),
		m.renewTimeout,
		fmt.Errorf(`refresh lease timeout`))
	defer cancel()

	err := l.Refresh(ctx, m.ttl)
	if err == nil {
		m.s.renewSuccess()
		return
	}
	m.s.renewFailed()
	m.log.Error(err, `Failed to renew lease.`)
}
