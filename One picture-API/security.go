package main

import (
	"crypto/sha256"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type TokenStore struct {
	hashes map[[32]byte]struct{}
}

func NewTokenStore(tokens []string) *TokenStore {
	store := &TokenStore{hashes: make(map[[32]byte]struct{})}
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		store.hashes[sha256.Sum256([]byte(token))] = struct{}{}
	}
	return store
}

func (ts *TokenStore) Len() int {
	if ts == nil {
		return 0
	}
	return len(ts.hashes)
}

func (ts *TokenStore) Valid(token string) bool {
	if ts == nil {
		return false
	}
	_, ok := ts.hashes[sha256.Sum256([]byte(strings.TrimSpace(token)))]
	return ok
}

type LoginRateLimiter struct {
	mu       sync.Mutex
	maxFails int
	window   time.Duration
	block    time.Duration
	items    map[string]*loginAttempt
}

type loginAttempt struct {
	firstFail    time.Time
	fails        int
	blockedUntil time.Time
}

func NewLoginRateLimiter(maxFails int, window, block time.Duration) *LoginRateLimiter {
	return &LoginRateLimiter{
		maxFails: maxFails,
		window:   window,
		block:    block,
		items:    make(map[string]*loginAttempt),
	}
}

func (rl *LoginRateLimiter) Allow(key string) (bool, time.Duration) {
	if rl == nil || rl.maxFails <= 0 {
		return true, 0
	}
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	item := rl.items[key]
	if item == nil {
		return true, 0
	}
	if !item.blockedUntil.IsZero() && now.Before(item.blockedUntil) {
		return false, time.Until(item.blockedUntil)
	}
	if now.Sub(item.firstFail) > rl.window {
		delete(rl.items, key)
	}
	return true, 0
}

func (rl *LoginRateLimiter) Failure(key string) (bool, time.Duration) {
	if rl == nil || rl.maxFails <= 0 {
		return true, 0
	}
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	item := rl.items[key]
	if item == nil || now.Sub(item.firstFail) > rl.window {
		item = &loginAttempt{firstFail: now}
		rl.items[key] = item
	}
	item.fails++
	if item.fails >= rl.maxFails {
		item.blockedUntil = now.Add(rl.block)
		return false, rl.block
	}
	return true, 0
}

func (rl *LoginRateLimiter) Success(key string) {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	delete(rl.items, key)
	rl.mu.Unlock()
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			for _, part := range strings.Split(xff, ",") {
				if ip := strings.TrimSpace(part); ip != "" {
					return ip
				}
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func requireWriteOrigin(w http.ResponseWriter, r *http.Request, trustedOrigins []string) bool {
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site == "cross-site" {
		writeAPIError(w, http.StatusForbidden, "FORBIDDEN_ORIGIN", "跨站请求被拒绝", "Sec-Fetch-Site=cross-site")
		return false
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if originAllowed(origin, r, trustedOrigins) {
		return true
	}
	writeAPIError(w, http.StatusForbidden, "FORBIDDEN_ORIGIN", "请求来源不允许", origin)
	return false
}

func originAllowed(origin string, r *http.Request, trustedOrigins []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	originHost := normalizeHost(u.Host)
	requestHost := normalizeHost(r.Host)
	if originHost != "" && originHost == requestHost {
		return true
	}

	originFull := strings.ToLower(u.Scheme + "://" + originHost)
	for _, trusted := range trustedOrigins {
		trusted = strings.ToLower(strings.TrimSpace(trusted))
		if trusted == "" {
			continue
		}
		if strings.Contains(trusted, "://") {
			if tu, err := url.Parse(trusted); err == nil {
				if originFull == strings.ToLower(tu.Scheme+"://"+normalizeHost(tu.Host)) {
					return true
				}
			}
			continue
		}
		if originHost == normalizeHost(trusted) {
			return true
		}
	}
	return false
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		if (p == "80") || (p == "443") {
			return strings.ToLower(h)
		}
		return strings.ToLower(net.JoinHostPort(h, p))
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
