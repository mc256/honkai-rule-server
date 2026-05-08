// Package clock provides a Clock interface so time-dependent code can be
// tested deterministically. Per Constitution Principle II + research R17.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time. Production wires RealClock; tests use FakeClock.
type Clock interface {
	Now() time.Time
}

// RealClock returns the system time.
type RealClock struct{}

// Now returns time.Now().
func (RealClock) Now() time.Time { return time.Now() }

// FakeClock is a manually-advanced clock for tests. Safe for concurrent
// Now/Advance from multiple goroutines.
type FakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

// NewFakeClock returns a FakeClock initialized to t.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

// Advance moves the fake clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set jumps the fake clock to t.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
