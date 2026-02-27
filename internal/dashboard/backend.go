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
)

// ErrClusterNotFound means the RedisCluster the portal shows does not exist.
var ErrClusterNotFound = errors.New("redis cluster not found")

// Backend is everything the portal needs from Kubernetes. It is deliberately
// narrow: the portal can read one RedisCluster, list and watch its Pods and
// events, and perform exactly the five changes its buttons offer. The same
// narrowness is mirrored in the RBAC the portal's ServiceAccount is given.
type Backend interface {
	// GetCluster returns the RedisCluster, or ErrClusterNotFound.
	GetCluster(ctx context.Context) (*ClusterView, error)
	// ListPods returns the cluster's Pods.
	ListPods(ctx context.Context) ([]PodView, error)
	// ListEvents returns recent operator events about the cluster, oldest first.
	ListEvents(ctx context.Context, limit int) ([]EventView, error)

	// WatchPods streams Pod changes until ctx ends or the watch breaks, then
	// closes the channel.
	WatchPods(ctx context.Context) (<-chan PodChange, error)
	// WatchEvents streams new operator events until ctx ends or the watch
	// breaks, then closes the channel.
	WatchEvents(ctx context.Context) (<-chan EventView, error)

	DeletePod(ctx context.Context, name string) error
	DeleteStatefulSet(ctx context.Context) error
	SetReplicas(ctx context.Context, replicas int32) error
	SetVersion(ctx context.Context, version string) error
}
