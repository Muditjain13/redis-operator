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
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// Main is the portal's entry point. cmd/dashboard only calls this, so that
// all the logic lives in a package that is tested and counted for coverage.
// It returns the process exit code.
func Main(args []string, static fs.FS, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fl.SetOutput(stderr)

	var (
		addr    = fl.String("addr", ":8080", "address to listen on")
		trust   = fl.Bool("trust-proxy-headers", false, "read the visitor address from CF-Connecting-IP (only behind Cloudflare)")
		cool    = fl.Duration("cooldown", 30*time.Second, "time one visitor must wait between actions")
		cfg     Config
		verbose = fl.Bool("verbose", false, "log at debug level")
	)
	fl.StringVar(&cfg.Namespace, "namespace", "demo", "namespace of the RedisCluster to show")
	fl.StringVar(&cfg.Cluster, "cluster", "rediscluster-sample", "name of the RedisCluster to show")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %v\n", fl.Args())
		return 2
	}
	cfg.Cooldown = *cool
	cfg.TrustProxyHeaders = *trust
	cfg.Static = static

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: level})))

	restConfig, err := config.GetConfig()
	if err != nil {
		slog.Error("Could not load the Kubernetes configuration", "error", err)
		return 1
	}
	backend, err := NewKubeBackend(restConfig, cfg.Namespace, cfg.Cluster)
	if err != nil {
		slog.Error("Could not create the Kubernetes client", "error", err)
		return 1
	}

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		slog.Error("Could not listen", "addr", *addr, "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("Starting portal", "addr", l.Addr().String(), "namespace", cfg.Namespace, "cluster", cfg.Cluster)
	if err := New(cfg, backend).Serve(ctx, l); err != nil {
		slog.Error("Portal stopped", "error", err)
		return 1
	}
	slog.Info("Portal stopped")
	return 0
}
