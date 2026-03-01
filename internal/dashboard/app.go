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
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"time"
)

// Config holds everything that can be tuned. All values have safe defaults.
type Config struct {
	// Namespace and Cluster identify the one RedisCluster the portal shows.
	Namespace string
	Cluster   string

	// Cooldown is how long one visitor must wait between actions.
	Cooldown time.Duration
	// TrustProxyHeaders makes the portal read the visitor address from the
	// CF-Connecting-IP header. Enable only behind Cloudflare.
	TrustProxyHeaders bool

	// The fixed targets of the scale and upgrade buttons.
	ScaleUpTo   int32
	ScaleDownTo int32
	UpgradeTo   string

	// RefreshInterval is the slowest the state is re-read.
	RefreshInterval time.Duration
	// HealTimeout is how long an action may take before it is reported as timed out.
	HealTimeout time.Duration
	// MaxStreams caps concurrent Server-Sent Event connections.
	MaxStreams int

	// Static is the built web UI. If nil, only the API is served.
	Static fs.FS
}

func (c Config) withDefaults() Config {
	if c.Namespace == "" {
		c.Namespace = "demo"
	}
	if c.Cluster == "" {
		c.Cluster = "rediscluster-sample"
	}
	if c.Cooldown == 0 {
		c.Cooldown = 30 * time.Second
	}
	if c.ScaleUpTo == 0 {
		c.ScaleUpTo = 5
	}
	if c.ScaleDownTo == 0 {
		c.ScaleDownTo = 3
	}
	if c.UpgradeTo == "" {
		c.UpgradeTo = "7.4"
	}
	if c.RefreshInterval == 0 {
		c.RefreshInterval = 2 * time.Second
	}
	if c.HealTimeout == 0 {
		c.HealTimeout = 5 * time.Minute
	}
	if c.MaxStreams == 0 {
		c.MaxStreams = 100
	}
	return c
}

// App is the portal backend: HTTP handlers plus the machinery behind them.
type App struct {
	cfg       Config
	backend   Backend
	tracker   *Tracker
	hub       *Hub
	limiter   *Limiter
	collector *Collector
	logger    *slog.Logger

	// pick returns a random number in [0, n). Replaceable in tests.
	pick func(n int) int
}

// New builds an App around backend.
func New(cfg Config, backend Backend) *App {
	cfg = cfg.withDefaults()
	tracker := NewTracker(cfg.HealTimeout)
	hub := NewHub(cfg.MaxStreams)
	return &App{
		cfg:       cfg,
		backend:   backend,
		tracker:   tracker,
		hub:       hub,
		limiter:   NewLimiter(cfg.Cooldown),
		collector: NewCollector(backend, tracker, hub, cfg.RefreshInterval),
		logger:    slog.Default(),
		pick:      rand.IntN,
	}
}

// Serve runs the portal on l until ctx ends, then shuts down gracefully.
func (a *App) Serve(ctx context.Context, l net.Listener) error {
	srv := &http.Server{
		Handler:           a.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       60 * time.Second,
		// No WriteTimeout: it would cut off the long-lived event streams.
		// Requests inherit ctx so streams end when the portal is stopping.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go a.collector.Run(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(l) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
