package httpapi

import (
	"sync"
	"time"
)

// limiter — простое скользящее окно на IP. Задача скромная: не дать одному
// клиенту залить сервер действиями. Для серьёзной защиты на VM есть
// limit_req в nginx (см. deploy/nginx.conf).
type limiter struct {
	limit  int
	window time.Duration

	mu     sync.Mutex
	hits   map[string][]time.Time
	lastGC time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, hits: map[string][]time.Time{}, lastGC: time.Now()}
}

func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastGC) > l.window {
		for k, times := range l.hits {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > l.window {
				delete(l.hits, k)
			}
		}
		l.lastGC = now
	}

	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
