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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type serverFixture struct {
	app   *App
	fb    *fakeBackend
	clock *fakeClock
	h     http.Handler
}

func newServerFixture(t *testing.T) *serverFixture {
	t.Helper()
	fb := newFakeBackend()
	app := newTestApp(fb)
	clock := newClock()
	app.limiter.now = clock.now
	app.tracker.now = clock.now
	if _, err := app.collector.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &serverFixture{app: app, fb: fb, clock: clock, h: app.Handler()}
}

// post sends an action request the way the web UI does.
func (f *serverFixture) post(action string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/actions/"+action, nil)
	r.RemoteAddr = "203.0.113.7:4000"
	r.Header.Set("X-Requested-With", requestedWithValue)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, w.Body.String())
	}
	return v
}

func TestStateEndpoint(t *testing.T) {
	f := newServerFixture(t)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/state", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	st := decode[State](t, w)
	if st.Cluster == nil || st.Cluster.Replicas != 3 || len(st.Pods) != 3 {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func TestHealthz(t *testing.T) {
	f := newServerFixture(t)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("healthz = %d %q", w.Code, w.Body.String())
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	f := newServerFixture(t)
	for _, path := range []string{"/healthz", "/api/state", "/api/nope", "/"} {
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		for header, want := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
			"Content-Security-Policy": contentSecurityPolicy,
		} {
			if got := w.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
	}
}

func TestActionAccepted(t *testing.T) {
	f := newServerFixture(t)
	w := f.post("kill-pod", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if op := decode[Operation](t, w); op.Action != ActionKillPod {
		t.Fatalf("op = %+v", op)
	}
	if len(f.fb.deleted) != 1 {
		t.Fatal("the Pod must have been deleted")
	}
}

func TestActionRequiresTheCSRFHeader(t *testing.T) {
	f := newServerFixture(t)
	for _, value := range []string{"", "XMLHttpRequest", "redis-portal2"} {
		r := httptest.NewRequest(http.MethodPost, "/api/actions/kill-pod", nil)
		r.RemoteAddr = "203.0.113.7:4000"
		if value != "" {
			r.Header.Set("X-Requested-With", value)
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("header %q: status = %d, want 403", value, w.Code)
		}
	}
	if len(f.fb.deleted) != 0 {
		t.Fatal("a forged request must not act")
	}
}

func TestActionUnknownAndWrongMethod(t *testing.T) {
	f := newServerFixture(t)
	if w := f.post("drop-database", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown action: status = %d", w.Code)
	}

	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/actions/kill-pod", nil))
	if w.Code != http.StatusMethodNotAllowed && w.Code != http.StatusNotFound {
		t.Fatalf("GET on an action: status = %d, want 405 or 404", w.Code)
	}
	if len(f.fb.deleted) != 0 {
		t.Fatal("nothing may act")
	}
}

func TestActionRateLimitedPerVisitor(t *testing.T) {
	f := newServerFixture(t)
	if w := f.post("scale-up", nil); w.Code != http.StatusAccepted {
		t.Fatalf("first action: status = %d, body %s", w.Code, w.Body.String())
	}

	f.clock.advance(10 * time.Second)
	w := f.post("kill-pod", nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second action: status = %d", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "20" {
		t.Fatalf("Retry-After = %q, want 20", got)
	}
	if body := decode[errorBody](t, w); body.RetryAfterSeconds != 20 {
		t.Fatalf("body = %+v", body)
	}

	// A different visitor is not limited by the first one (the tracker is busy
	// with the first action, which is a conflict, not a rate limit).
	r := httptest.NewRequest(http.MethodPost, "/api/actions/kill-pod", nil)
	r.RemoteAddr = "198.51.100.9:4000"
	r.Header.Set("X-Requested-With", requestedWithValue)
	other := httptest.NewRecorder()
	f.h.ServeHTTP(other, r)
	if other.Code != http.StatusConflict {
		t.Fatalf("other visitor: status = %d, want 409 (busy), body %s", other.Code, other.Body.String())
	}
}

func TestRateLimitUsesProxyHeaderOnlyWhenTrusted(t *testing.T) {
	for _, trust := range []bool{false, true} {
		f := newServerFixture(t)
		f.app.cfg.TrustProxyHeaders = trust
		f.post("scale-up", map[string]string{"CF-Connecting-IP": "198.51.100.1"})
		f.clock.advance(31 * time.Second)
		f.fb.update(func(b *fakeBackend) { // let the next action run
			b.cluster = settledCluster(5)
			b.pods = readyPods(5)
		})
		f.app.collector.Refresh(context.Background()) //nolint:errcheck // completes the first action

		// Same connection address, different claimed address.
		w := f.post("scale-down", map[string]string{"CF-Connecting-IP": "198.51.100.2"})
		wantRateLimited := false // the cooldown has passed either way
		if got := w.Code == http.StatusTooManyRequests; got != wantRateLimited {
			t.Fatalf("trust=%v: rate limited = %v", trust, got)
		}
	}

	// Spoofing the header must not let an untrusted caller dodge the limit.
	f := newServerFixture(t)
	f.post("scale-up", map[string]string{"CF-Connecting-IP": "198.51.100.1"})
	w := f.post("scale-down", map[string]string{"CF-Connecting-IP": "198.51.100.2"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("untrusted header must be ignored; status = %d", w.Code)
	}
}

func TestActionConflictsDoNotCostACooldown(t *testing.T) {
	f := newServerFixture(t)

	// Already at 3 replicas: scale-down is a no-op, reported as 409.
	if w := f.post("scale-down", nil); w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	// The visitor was not charged, so a real action works straight away.
	if w := f.post("kill-pod", nil); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body %s", w.Code, w.Body.String())
	}
}

func TestActionBackendFailureIsGenericAndRefunded(t *testing.T) {
	f := newServerFixture(t)
	f.fb.update(func(b *fakeBackend) { b.actionErr = errors.New("secret apiserver detail: token abc123") })

	w := f.post("scale-up", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "abc123") || strings.Contains(w.Body.String(), "apiserver") {
		t.Fatalf("internal detail leaked to the client: %s", w.Body.String())
	}

	f.fb.update(func(b *fakeBackend) { b.actionErr = nil })
	if w := f.post("scale-up", nil); w.Code != http.StatusAccepted {
		t.Fatalf("after a failure the visitor must be able to retry; status = %d", w.Code)
	}
}

func TestActionWhenClusterIsMissing(t *testing.T) {
	f := newServerFixture(t)
	f.fb.update(func(b *fakeBackend) { b.cluster = nil })
	if w := f.post("kill-pod", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

// sseStream reads Server-Sent Events from an open response with one
// goroutine, so a test can take events one at a time.
type sseStream struct {
	resp *http.Response
	msgs chan Message
}

func openSSE(t *testing.T, url string) *sseStream {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	s := &sseStream{resp: resp, msgs: make(chan Message, 16)}
	if resp.StatusCode != http.StatusOK {
		return s
	}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var cur Message
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.Type = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.Data = []byte(strings.TrimPrefix(line, "data: "))
			case line == "" && cur.Type != "":
				s.msgs <- cur
				cur = Message{}
			}
		}
	}()
	return s
}

func (s *sseStream) next(t *testing.T) Message {
	t.Helper()
	select {
	case m := <-s.msgs:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Message{}
	}
}

func TestStreamSendsStateThenLiveMessages(t *testing.T) {
	f := newServerFixture(t)
	srv := httptest.NewServer(f.h)
	t.Cleanup(srv.Close) // runs after the stream bodies are closed

	stream := openSSE(t, srv.URL+"/api/stream")
	if ct := stream.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	first := stream.next(t)
	if first.Type != "state" {
		t.Fatalf("first event = %q, want state", first.Type)
	}
	var st State
	if err := json.Unmarshal(first.Data, &st); err != nil || st.Cluster == nil {
		t.Fatalf("bad state payload: %v %s", err, first.Data)
	}

	// The handler has subscribed by now (it sent the first event after subscribing).
	f.app.hub.Publish(Message{Type: "heal", Data: []byte(`{"action":"kill-pod","seconds":12.5}`)})
	if second := stream.next(t); second.Type != "heal" || !strings.Contains(string(second.Data), "12.5") {
		t.Fatalf("second event = %+v", second)
	}
}

func TestStreamCapReturns503(t *testing.T) {
	fb := newFakeBackend()
	app := New(Config{MaxStreams: 1}, fb)
	if _, err := app.collector.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close) // runs after the stream bodies are closed

	first := openSSE(t, srv.URL+"/api/stream")
	first.next(t) // the first stream is established

	second := openSSE(t, srv.URL+"/api/stream")
	if second.resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", second.resp.StatusCode)
	}
}

func staticApp(files fstest.MapFS) http.Handler {
	app := New(Config{Static: files}, newFakeBackend())
	return app.Handler()
}

func TestStaticServing(t *testing.T) {
	h := staticApp(fstest.MapFS{
		"index.html":      {Data: []byte("<html>portal</html>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		"favicon.svg":     {Data: []byte("<svg/>")},
	})

	tests := []struct {
		path      string
		wantCode  int
		wantBody  string
		wantCache string
	}{
		{"/", 200, "portal", "no-cache"},
		{"/index.html", 301, "", ""}, // http.ServeFile canonicalises to "/"
		{"/assets/app-1.js", 200, "console.log", "public, max-age=31536000, immutable"},
		{"/favicon.svg", 200, "<svg/>", "no-cache"},
		{"/some/client/route", 200, "portal", "no-cache"},
		{"/assets/missing.js", 404, "", ""},
		{"/missing.png", 404, "", ""},
		{"/api/unknown", 404, "", ""},
		{"/assets", 200, "portal", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantCode)
			}
			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", w.Body.String(), tt.wantBody)
			}
			if tt.wantCache != "" && w.Header().Get("Cache-Control") != tt.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), tt.wantCache)
			}
		})
	}
}

func TestNoStaticFilesMeansNotFound(t *testing.T) {
	h := New(Config{}, newFakeBackend()).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestStaticPathTraversalIsContained(t *testing.T) {
	h := staticApp(fstest.MapFS{"index.html": {Data: []byte("portal")}, "secret.txt": {Data: []byte("nope")}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil))
	if strings.Contains(w.Body.String(), "root:") {
		t.Fatal("path traversal escaped the static filesystem")
	}
}

func TestConfigDefaults(t *testing.T) {
	app := New(Config{}, newFakeBackend())
	c := app.cfg
	if c.Namespace != "demo" || c.Cluster != "rediscluster-sample" || c.Cooldown != 30*time.Second ||
		c.ScaleUpTo != 5 || c.ScaleDownTo != 3 || c.UpgradeTo != "7.4" || c.MaxStreams != 100 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if override := New(Config{ScaleUpTo: 6}, newFakeBackend()).cfg; override.ScaleUpTo != 6 {
		t.Fatalf("explicit values must win: %+v", override)
	}
}
