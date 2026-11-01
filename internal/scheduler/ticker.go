package scheduler

import "time"

// ticker abstracts time.Ticker so tests can drive scheduling deterministically
// instead of sleeping on the wall clock.
type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// tickerFactory builds a ticker for a given interval. Swapped out in tests.
type tickerFactory func(d time.Duration) ticker

func realTickerFactory(d time.Duration) ticker {
	return realTicker{t: time.NewTicker(d)}
}
