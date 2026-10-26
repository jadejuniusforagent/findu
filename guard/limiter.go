// Package guard — limiter: a tiny fixed-window per-host rate limiter.
//
// capkit 的 Limiter.Acquire(host, spec) 的最简落法：按目标主机（capkit 语义：按 host）
// 计数放行。固定 60s 窗口（分钟级精度足够门禁用，不需要令牌桶的平滑性）；
// 线程安全；容量受 sweep 上限保护。
package guard

import (
	"sync"
	"time"
)

type hostBucket struct {
	windowStart int64 // unix seconds
	count       int
}

type rateLimiter struct {
	mu        sync.Mutex
	perMinute int
	hosts     map[string]*hostBucket
}

func newRateLimiter(perMinute int) *rateLimiter {
	return &rateLimiter{perMinute: perMinute, hosts: map[string]*hostBucket{}}
}

// allow reports whether host may proceed this window. cap=0 disables.
func (l *rateLimiter) allow(host string) (allowed bool, limit int) {
	if l == nil || l.perMinute <= 0 || host == "" {
		return true, 0
	}
	now := time.Now().Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hosts) > 8192 {
		l.sweepLocked(now)
	}
	b, ok := l.hosts[host]
	if !ok || now-b.windowStart >= 60 {
		l.hosts[host] = &hostBucket{windowStart: now, count: 1}
		return true, l.perMinute
	}
	if b.count >= l.perMinute {
		return false, l.perMinute
	}
	b.count++
	return true, l.perMinute
}

// sweepLocked drops hosts idle for over 10 minutes. Called under lock.
func (l *rateLimiter) sweepLocked(now int64) {
	for h, b := range l.hosts {
		if now-b.windowStart > 600 {
			delete(l.hosts, h)
			if len(l.hosts) <= 4096 {
				return
			}
		}
	}
}
