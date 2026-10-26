package contextmgmt

import (
	"sync"
	"time"
)

// Breaker stops repeated fold attempts after consecutive failures (fail-open, never a loop)
// and probes once more after a cooldown. Pure logic, no IO; the clock is injectable for tests.
type Breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	failures  int
	openUntil time.Time
	now       func() time.Time
}

// NewBreaker builds a breaker. threshold<1 becomes 3; cooldown<=0 becomes 5 minutes.
func NewBreaker(threshold int, cooldown time.Duration) *Breaker {
	if threshold < 1 {
		threshold = 3
	}
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}
	return &Breaker{threshold: threshold, cooldown: cooldown, now: time.Now}
}

// Allow reports whether an attempt may proceed: closed means yes; open means yes only after
// the cooldown elapsed (half-open probe).
func (b *Breaker) Allow() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.threshold {
		return true
	}
	return b.now().After(b.openUntil)
}

// Fail records a failure; reaching the threshold opens the breaker for the cooldown window.
func (b *Breaker) Fail() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = b.now().Add(b.cooldown)
	}
}

// Success clears the failure streak (a successful half-open probe closes the breaker).
func (b *Breaker) Success() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
}

// SetNow injects a clock (tests only).
func (b *Breaker) SetNow(now func() time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
}
