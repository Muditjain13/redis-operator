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
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// specResetter is the one thing the reset job needs from Kubernetes.
type specResetter interface {
	ResetSpec(ctx context.Context, replicas int32, version string) (bool, error)
}

// runReset puts the demo cluster back to its starting spec.
func runReset(ctx context.Context, r specResetter, replicas int32, version string) error {
	changed, err := r.ResetSpec(ctx, replicas, version)
	if err != nil {
		return err
	}
	if changed {
		slog.Info("Reset RedisCluster", "replicas", replicas, "version", version)
	} else {
		slog.Info("RedisCluster already at the starting spec", "replicas", replicas, "version", version)
	}
	return nil
}

// resetMain is the "reset" subcommand, run by the CronJob. It returns the
// process exit code.
func resetMain(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("dashboard reset", flag.ContinueOnError)
	fl.SetOutput(stderr)
	var (
		namespace = fl.String("namespace", "demo", "namespace of the RedisCluster")
		cluster   = fl.String("cluster", "rediscluster-sample", "name of the RedisCluster")
		replicas  = fl.Int("replicas", 3, "replica count to restore")
		version   = fl.String("version", "7.2", "Redis version to restore")
		timeout   = fl.Duration("timeout", 30*time.Second, "give up after this long")
	)
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %v\n", fl.Args())
		return 2
	}
	if *replicas < 1 || *replicas > 7 {
		_, _ = fmt.Fprintln(stderr, "replicas must be between 1 and 7")
		return 2
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stdout, nil)))

	restConfig, err := config.GetConfig()
	if err != nil {
		slog.Error("Could not load the Kubernetes configuration", "error", err)
		return 1
	}
	backend, err := NewKubeBackend(restConfig, *namespace, *cluster)
	if err != nil {
		slog.Error("Could not create the Kubernetes client", "error", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := runReset(ctx, backend, int32(*replicas), *version); err != nil {
		slog.Error("Could not reset RedisCluster", "error", err)
		return 1
	}
	return 0
}
