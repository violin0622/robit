package robit

import (
	"time"

	"github.com/go-logr/logr"
)

type Option[ID any] func(*Mutex[ID])

func WithLogr[ID any](l logr.Logger) Option[ID] {
	return func(m *Mutex[ID]) { m.log = l }
}

func WithRenewPeriod[ID any](p time.Duration) Option[ID] {
	return func(m *Mutex[ID]) { m.renewPeriod = p }
}

func WithRenewTimeout[ID any](to time.Duration) Option[ID] {
	return func(m *Mutex[ID]) { m.renewTimeout = to }
}

func WithOnLost[ID any](fn ...func()) Option[ID] {
	return func(m *Mutex[ID]) {
		m.onLost = append(m.onLost, fn...)
	}
}
