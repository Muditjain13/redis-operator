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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	// requestedWithValue must accompany every POST. A browser will not add a
	// custom header to a cross-site request without a CORS preflight, which
	// the portal never approves, so this blocks cross-site request forgery.
	requestedWithValue = "redis-portal"

	// keepAliveInterval keeps idle event streams from being closed by proxies.
	keepAliveInterval = 15 * time.Second

	contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

// Handler returns the portal's HTTP handler.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("GET /api/stream", a.handleStream)
	mux.HandleFunc("POST /api/actions/{action}", a.handleAction)
	mux.Handle("GET /", a.staticHandler())
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleState(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, a.collector.Snapshot())
}

// handleStream sends Server-Sent Events: a full "state" snapshot first, then
// further "state" snapshots and "pod", "event" and "heal" messages as they happen.
func (a *App) handleStream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// Event streams live as long as the page is open.
	_ = rc.SetWriteDeadline(time.Time{})

	ch, unsubscribe, err := a.hub.Subscribe()
	if err != nil {
		http.Error(w, "too many open streams, try again shortly", http.StatusServiceUnavailable)
		return
	}
	defer unsubscribe()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // do not let nginx-style proxies buffer the stream

	state, err := json.Marshal(a.collector.Snapshot())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := fmt.Fprintf(w, "retry: 3000\n"); err != nil {
		return
	}
	if err := writeEvent(w, Message{Type: "state", Data: state}); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	keepAlive := time.NewTicker(keepAliveInterval)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			if err := writeEvent(w, m); err != nil {
				return
			}
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, m Message) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.Type, m.Data)
	return err
}

type errorBody struct {
	Error             string `json:"error"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Requested-With") != requestedWithValue {
		writeJSON(w, http.StatusForbidden, errorBody{Error: "missing X-Requested-With header"})
		return
	}
	action, err := ParseAction(r.PathValue("action"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "unknown action"})
		return
	}
	// Actions take no input, so there is nothing to read from the body.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

	key := ClientKey(r, a.cfg.TrustProxyHeaders)
	if ok, wait := a.limiter.Allow(key); !ok {
		secs := int(math.Ceil(wait.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeJSON(w, http.StatusTooManyRequests, errorBody{
			Error:             fmt.Sprintf("please wait %d seconds before the next action", secs),
			RetryAfterSeconds: secs,
		})
		return
	}

	op, err := a.Perform(r.Context(), action)
	if err != nil {
		// The action did not run, so do not charge the visitor a cooldown.
		a.limiter.Refund(key)
		a.writeActionError(w, action, err)
		return
	}
	writeJSON(w, http.StatusAccepted, op)
}

func (a *App) writeActionError(w http.ResponseWriter, action Action, err error) {
	switch {
	case errors.Is(err, ErrBusy), errors.Is(err, ErrNoop):
		writeJSON(w, http.StatusConflict, errorBody{Error: err.Error()})
	case errors.Is(err, ErrNoCluster):
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: err.Error()})
	default:
		// Log the detail; do not hand cluster internals to a stranger.
		a.logger.Error("Action failed", "action", action, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "the action failed, please try again"})
	}
}

// staticHandler serves the built web UI. Unknown paths without a file
// extension fall back to index.html so client-side routes work; a missing
// asset is a real 404 rather than HTML pretending to be a script.
func (a *App) staticHandler() http.Handler {
	if a.cfg.Static == nil {
		return http.NotFoundHandler()
	}
	// Serve files only from within the configured filesystem.
	root := a.cfg.Static
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An unknown API path is an error, never the web page.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(root, name); err != nil || info.IsDir() {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}

		if strings.HasPrefix(name, "assets/") {
			// Build output is content-hashed, so it can be cached forever.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, root, name)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
