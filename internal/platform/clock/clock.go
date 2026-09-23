// Package clock abstracts "now" so time-dependent code can be tested.
//
// Domain code never calls time.Now(): it receives `now` as a parameter.
// Application services get a Clock and pass clock.Now() down. Tests use Fake
// to freeze or move time, so "the OTP expired after 5 minutes" is a
// deterministic test instead of a 5-minute sleep.
package clock

import (
	"sync"
	"time"
)

// Clock tells the current time.
type Clock interface {
	Now() time.Time
}

// System is the real clock, always in UTC.
type System struct{}

// Now returns the current time in UTC.
func (System) Now() time.Time { return time.Now().UTC() }

// Fake is a clock for tests: it only moves when told to. Safe for concurrent use.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a Fake frozen at t.
func NewFake(t time.Time) *Fake { return &Fake{now: t.UTC()} }

// Now returns the frozen time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

// Advance moves the clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Compile-time checks that both types satisfy Clock.
var (
	_ Clock = System{}
	_ Clock = (*Fake)(nil)
)
