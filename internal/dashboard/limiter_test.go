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
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func TestLimiterCooldown(t *testing.T) {
	clock := newClock()
	l := NewLimiter(30 * time.Second)
	l.now = clock.now

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first action must be allowed")
	}

	clock.advance(10 * time.Second)
	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("second action inside the cooldown must be refused")
	}
	if wait != 20*time.Second {
		t.Fatalf("retryAfter = %s, want 20s", wait)
	}

	// Another visitor is not affected.
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("a different key has its own cooldown")
	}

	clock.advance(20 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("action must be allowed once the cooldown has passed")
	}
}

func TestLimiterRefundRestoresTheAction(t *testing.T) {
	clock := newClock()
	l := NewLimiter(30 * time.Second)
	l.now = clock.now

	l.Allow("a")
	l.Refund("a")
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a refunded action must not count against the visitor")
	}
}

func TestLimiterPurgesExpiredEntries(t *testing.T) {
	clock := newClock()
	l := NewLimiter(30 * time.Second)
	l.now = clock.now

	for i := range purgeThreshold + 10 {
		l.Allow("old-" + strconv.Itoa(i))
	}
	clock.advance(time.Minute)
	l.Allow("fresh")

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.last) != 1 {
		t.Fatalf("expected expired entries to be purged, %d remain", len(l.last))
	}
}

func TestClientKey(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		cfHeader   string
		trustProxy bool
		want       string
	}{
		{"plain IPv4", "203.0.113.7:51234", "", false, "203.0.113.7"},
		{"header ignored when not trusted", "203.0.113.7:51234", "198.51.100.9", false, "203.0.113.7"},
		{"header used when trusted", "10.0.0.1:1234", "198.51.100.9", true, "198.51.100.9"},
		{"bad header falls back to the connection", "10.0.0.1:1234", "not-an-ip", true, "10.0.0.1"},
		{"no header falls back to the connection", "10.0.0.1:1234", "", true, "10.0.0.1"},
		{"IPv6 is reduced to its /64", "[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1234", "", false, "2001:db8:1:2::/64"},
		{"addresses in one /64 share a key", "[2001:db8:1:2:1111:2222:3333:4444]:99", "", false, "2001:db8:1:2::/64"},
		{"IPv4-mapped IPv6 is treated as IPv4", "[::ffff:203.0.113.7]:80", "", false, "203.0.113.7"},
		{"unparseable remote is used verbatim", "pipe", "", false, "pipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.cfHeader != "" {
				r.Header.Set("CF-Connecting-IP", tt.cfHeader)
			}
			if got := ClientKey(r, tt.trustProxy); got != tt.want {
				t.Fatalf("ClientKey = %q, want %q", got, tt.want)
			}
		})
	}
}
