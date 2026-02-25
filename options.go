package robit

import (
	"time"

	"github.com/go-logr/logr"
)

type Option[ID comparable] func(*Mutex[ID])

func WithLogr[ID comparable](l logr.Logger) Option[ID] {
	return func(m *Mutex[ID]) { m.log = l }
}

func WithRenewPeriod[ID comparable](p time.Duration) Option[ID] {
	return func(m *Mutex[ID]) { m.renewPeriod = p }
}

func WithRenewTimeout[ID comparable](to time.Duration) Option[ID] {
	return func(m *Mutex[ID]) { m.renewTimeout = to }
}

func WithOnLost[ID comparable](fn ...func()) Option[ID] {
	return func(m *Mutex[ID]) {
		m.onLost = append(m.onLost, fn...)
	}
}
