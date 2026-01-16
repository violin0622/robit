package robit

import (
	"context"
	"time"
)

type Driver[ID any] interface {
	Acquire(context.Context, ID, time.Duration) (Lease, error)
	Release(Lease)
}

type Lease interface {
	Refresh(context.Context, time.Duration) error
	Lost() <-chan struct{}
}
