package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type LoginLimiter struct {
	mu      sync.Mutex
	entries map[string]loginFail
}

type loginFail struct {
	Count       int
	UpdatedAt   time.Time
	LockedUntil time.Time
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{entries: make(map[string]loginFail)}
}

func (l *LoginLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[ip]
	if entry.LockedUntil.After(time.Now()) {
		return false
	}
	return true
}

func (l *LoginLimiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry := l.entries[ip]
	if now.Sub(entry.UpdatedAt) > 10*time.Minute {
		entry = loginFail{}
	}
	entry.Count++
	entry.UpdatedAt = now
	if entry.Count >= 5 {
		entry.LockedUntil = now.Add(60 * time.Second)
	}
	l.entries[ip] = entry
}

func (l *LoginLimiter) Success(ip string) {
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}

func ClientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
