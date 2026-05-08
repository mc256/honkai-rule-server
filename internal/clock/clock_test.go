package clock

import (
	"sync"
	"testing"
	"time"
)

func TestRealClock_NowReturnsCurrentTime(t *testing.T) {
	before := time.Now()
	got := RealClock{}.Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Errorf("RealClock.Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestFakeClock_NowReturnsInitial(t *testing.T) {
	t0 := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(t0)
	if got := c.Now(); !got.Equal(t0) {
		t.Errorf("FakeClock.Now() = %v, want %v", got, t0)
	}
}

func TestFakeClock_Advance(t *testing.T) {
	t0 := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(t0)
	c.Advance(2 * time.Hour)
	want := t0.Add(2 * time.Hour)
	if got := c.Now(); !got.Equal(want) {
		t.Errorf("after Advance(2h) Now() = %v, want %v", got, want)
	}
}

func TestFakeClock_Set(t *testing.T) {
	c := NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	target := time.Date(2027, 6, 15, 9, 30, 0, 0, time.UTC)
	c.Set(target)
	if got := c.Now(); !got.Equal(target) {
		t.Errorf("after Set() Now() = %v, want %v", got, target)
	}
}

// TestFakeClock_ConcurrentNowAndAdvance is a simple race-detector check.
// Run with `go test -race`.
func TestFakeClock_ConcurrentNowAndAdvance(t *testing.T) {
	c := NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = c.Now()
		}()
		go func() {
			defer wg.Done()
			c.Advance(time.Second)
		}()
	}
	wg.Wait()
}
