package auth

import (
	"sync"
	"time"
)

// attemptLimiter is a fixed-window in-memory limiter used to throttle login
// attempts per client IP and per email address.
type attemptLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	window time.Duration
	max    int
}

func newAttemptLimiter(window time.Duration, max int) *attemptLimiter {
	return &attemptLimiter{
		hits:   map[string][]time.Time{},
		window: window,
		max:    max,
	}
}

func (l *attemptLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	hits := l.hits[key]
	fresh := hits[:0]
	for _, t := range hits {
		if now.Sub(t) < l.window {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= l.max {
		l.hits[key] = fresh
		l.pruneLocked(now)
		return false
	}
	l.hits[key] = append(fresh, now)
	l.pruneLocked(now)
	return true
}

func (l *attemptLimiter) pruneLocked(now time.Time) {
	if len(l.hits) < 4096 {
		return
	}
	for k, hits := range l.hits {
		fresh := hits[:0]
		for _, t := range hits {
			if now.Sub(t) < l.window {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) == 0 {
			delete(l.hits, k)
		} else {
			l.hits[k] = fresh
		}
	}
}
