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
	"sync"
)

// fakeBackend is a Backend whose state the tests control. Its methods read the
// same fields the tests write, so a test can "make Kubernetes do something" by
// changing a field and then asking the app to refresh.
type fakeBackend struct {
	mu sync.Mutex

	cluster    *ClusterView
	clusterErr error
	pods       []PodView
	podsErr    error
	events     []EventView
	eventsErr  error

	podWatch   chan PodChange
	eventWatch chan EventView
	watchErr   error

	actionErr error
	deleted   []string
	stsDelete int
	replicas  []int32
	versions  []string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		cluster:    settledCluster(3),
		pods:       readyPods(3),
		podWatch:   make(chan PodChange, 8),
		eventWatch: make(chan EventView, 8),
	}
}

func (f *fakeBackend) GetCluster(context.Context) (*ClusterView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.clusterErr != nil {
		return nil, f.clusterErr
	}
	if f.cluster == nil {
		return nil, ErrClusterNotFound
	}
	c := *f.cluster
	c.Conditions = append([]ConditionView{}, f.cluster.Conditions...)
	return &c, nil
}

func (f *fakeBackend) ListPods(context.Context) ([]PodView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.podsErr != nil {
		return nil, f.podsErr
	}
	return append([]PodView{}, f.pods...), nil
}

func (f *fakeBackend) ListEvents(context.Context, int) ([]EventView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]EventView{}, f.events...), f.eventsErr
}

func (f *fakeBackend) WatchPods(context.Context) (<-chan PodChange, error) {
	if f.watchErr != nil {
		return nil, f.watchErr
	}
	return f.podWatch, nil
}

func (f *fakeBackend) WatchEvents(context.Context) (<-chan EventView, error) {
	if f.watchErr != nil {
		return nil, f.watchErr
	}
	return f.eventWatch, nil
}

func (f *fakeBackend) DeletePod(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.actionErr != nil {
		return f.actionErr
	}
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *fakeBackend) DeleteStatefulSet(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.actionErr != nil {
		return f.actionErr
	}
	f.stsDelete++
	return nil
}

func (f *fakeBackend) SetReplicas(_ context.Context, n int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.actionErr != nil {
		return f.actionErr
	}
	f.replicas = append(f.replicas, n)
	return nil
}

func (f *fakeBackend) SetVersion(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.actionErr != nil {
		return f.actionErr
	}
	f.versions = append(f.versions, v)
	return nil
}

// update lets a test change several fields atomically.
func (f *fakeBackend) update(change func(*fakeBackend)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// settledCluster is a healthy cluster: n replicas, all ready, no rollout.
func settledCluster(n int32) *ClusterView {
	return &ClusterView{
		Name: "rediscluster-sample", Namespace: "demo",
		Replicas: n, Version: "7.2", ReadyReplicas: n, CurrentVersion: "7.2",
		StatefulSetUID: "sts-1",
		Conditions: []ConditionView{
			{Type: "Available", Status: "True"},
			{Type: "Progressing", Status: "False"},
			{Type: "Degraded", Status: "False"},
		},
	}
}

func readyPods(n int) []PodView {
	pods := make([]PodView, 0, n)
	for i := range n {
		pods = append(pods, PodView{
			Name:  podName(i),
			UID:   "uid-" + podName(i),
			State: PodReady,
			Image: "redis:7.2",
		})
	}
	return pods
}

func podName(i int) string { return "rediscluster-sample-" + string(rune('0'+i)) }
