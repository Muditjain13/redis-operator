/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dashboard

import (
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// purgeThreshold is the map size above which expired entries are removed.
const purgeThreshold = 1024

// Limiter allows one action per key every cooldown. The portal uses the
// client address as the key.
type Limiter struct {
	cooldown time.Duration
	now      func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

// NewLimiter returns a Limiter with the given cooldown.
func NewLimiter(cooldown time.Duration) *Limiter {
	return &Limiter{cooldown: cooldown, now: time.Now, last: map[string]time.Time{}}
}

// Allow reports whether key may act now. If it may, the cooldown starts
// immediately. If it may not, retryAfter says how long to wait.
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if len(l.last) > purgeThreshold {
		for k, t := range l.last {
			if now.Sub(t) >= l.cooldown {
				delete(l.last, k)
			}
		}
	}

	if t, ok := l.last[key]; ok {
		if wait := l.cooldown - now.Sub(t); wait > 0 {
			return false, wait
		}
	}
	l.last[key] = now
	return true, 0
}

// Refund cancels the cooldown that Allow started. It is used when the action
// turned out not to run (a conflict or an error), so that the visitor is not
// punished for it.
func (l *Limiter) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.last, key)
}

// ClientKey identifies the visitor making r.
//
// By default it is the connection's remote address. When trustProxy is true
// the CF-Connecting-IP header set by Cloudflare is used instead, which is only
// correct when every request really arrives through Cloudflare; otherwise a
// client could choose its own key and bypass the limit. IPv6 addresses are
// reduced to their /64, because a single customer typically controls a whole
// /64 and could otherwise rotate through it.
func ClientKey(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if addr, err := netip.ParseAddr(r.Header.Get("CF-Connecting-IP")); err == nil {
			return keyFor(addr)
		}
	}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return keyFor(ap.Addr())
	}
	return r.RemoteAddr
}

func keyFor(addr netip.Addr) string {
	addr = addr.Unmap()
	if addr.Is6() {
		if prefix, err := addr.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return addr.String()
}
