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
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

const (
	testNS      = "demo"
	testCluster = "rediscluster-sample"
)

func newKubeFixture(t *testing.T) *KubeBackend {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := cachev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	rc := &cachev1alpha1.RedisCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testCluster, Namespace: testNS},
		Spec: cachev1alpha1.RedisClusterSpec{
			Replicas: 3, Version: "7.2",
		},
		Status: cachev1alpha1.RedisClusterStatus{
			ReadyReplicas: 3, CurrentVersion: "7.2",
			Conditions: []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue}},
		},
	}
	return &KubeBackend{
		CR:        crfake.NewClientBuilder().WithScheme(scheme).WithObjects(rc).Build(),
		Clientset: kubefake.NewClientset(),
		Namespace: testNS,
		Cluster:   testCluster,
	}
}

func clusterPod(name, instance string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: testNS, UID: types.UID("uid-" + name),
			Labels: map[string]string{"app.kubernetes.io/name": "redis", "app.kubernetes.io/instance": instance},
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Image: "redis:7.2"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func event(name, kind, regarding, reason string, at time.Time) *eventsv1.Event {
	return &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Regarding:  corev1.ObjectReference{Kind: kind, Name: regarding},
		Reason:     reason, Note: reason + " happened", Type: "Normal",
		EventTime: metav1.NewMicroTime(at),
	}
}

func TestKubeGetCluster(t *testing.T) {
	b := newKubeFixture(t)
	ctx := context.Background()

	// Without a StatefulSet (e.g. mid-demo) the UID is empty.
	got, err := b.GetCluster(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Replicas != 3 || got.Version != "7.2" || got.ReadyReplicas != 3 || got.StatefulSetUID != "" {
		t.Fatalf("unexpected view: %+v", got)
	}
	if got.ConditionStatus("Available") != "True" {
		t.Fatalf("conditions = %+v", got.Conditions)
	}

	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: testCluster, Namespace: testNS, UID: "sts-uid"}}
	if _, err := b.Clientset.AppsV1().StatefulSets(testNS).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ = b.GetCluster(ctx); got.StatefulSetUID != "sts-uid" {
		t.Fatalf("StatefulSetUID = %q", got.StatefulSetUID)
	}
}

func TestKubeGetClusterNotFound(t *testing.T) {
	b := newKubeFixture(t)
	b.Cluster = "missing"
	if _, err := b.GetCluster(context.Background()); !errors.Is(err, ErrClusterNotFound) {
		t.Fatalf("err = %v, want ErrClusterNotFound", err)
	}
}

func TestKubeListPodsFiltersAndSorts(t *testing.T) {
	b := newKubeFixture(t)
	ctx := context.Background()
	for _, p := range []*corev1.Pod{
		clusterPod("rediscluster-sample-2", testCluster),
		clusterPod("rediscluster-sample-0", testCluster),
		clusterPod("other-0", "another-cluster"),
	} {
		if _, err := b.Clientset.CoreV1().Pods(testNS).Create(ctx, p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	pods, err := b.ListPods(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 2 || pods[0].Name != "rediscluster-sample-0" || pods[1].Name != "rediscluster-sample-2" {
		t.Fatalf("pods = %+v", pods)
	}
}

func TestKubeListEvents(t *testing.T) {
	b := newKubeFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, e := range []*eventsv1.Event{
		event("e3", "RedisCluster", testCluster, "Healed", base.Add(3*time.Minute)),
		event("e1", "RedisCluster", testCluster, "Created", base),
		event("e2", "RedisCluster", testCluster, "Scaled", base.Add(time.Minute)),
		event("x1", "RedisCluster", "another-cluster", "Created", base),
		event("x2", "Pod", testCluster, "Pulled", base),
	} {
		if _, err := b.Clientset.EventsV1().Events(testNS).Create(ctx, e, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := b.ListEvents(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Reason != "Created" || all[2].Reason != "Healed" {
		t.Fatalf("expected our 3 events oldest first, got %+v", all)
	}

	last2, _ := b.ListEvents(ctx, 2)
	if len(last2) != 2 || last2[0].Reason != "Scaled" || last2[1].Reason != "Healed" {
		t.Fatalf("limit must keep the newest events, got %+v", last2)
	}
}

func TestEventTimePrefersTheMostPreciseField(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t1.Add(2 * time.Hour)
	t4 := t1.Add(3 * time.Hour)

	e := &eventsv1.Event{
		ObjectMeta:              metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(t4)},
		DeprecatedLastTimestamp: metav1.NewTime(t3),
		EventTime:               metav1.NewMicroTime(t2),
		Series:                  &eventsv1.EventSeries{LastObservedTime: metav1.NewMicroTime(t1.Add(-time.Hour))},
	}
	if got := eventTime(e); !got.Equal(t1.Add(-time.Hour)) {
		t.Fatalf("series time should win, got %s", got)
	}
	e.Series = nil
	if got := eventTime(e); !got.Equal(t2) {
		t.Fatalf("event time should win next, got %s", got)
	}
	e.EventTime = metav1.MicroTime{}
	if got := eventTime(e); !got.Equal(t3) {
		t.Fatalf("deprecated timestamp should win next, got %s", got)
	}
	e.DeprecatedLastTimestamp = metav1.Time{}
	if got := eventTime(e); !got.Equal(t4) {
		t.Fatalf("creation time is the last resort, got %s", got)
	}
}

func TestKubeWatchPods(t *testing.T) {
	b := newKubeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := b.WatchPods(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Clientset.CoreV1().Pods(testNS).Create(ctx, clusterPod("rediscluster-sample-0", testCluster), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	select {
	case change := <-ch:
		if change.Type != "ADDED" || change.Pod.Name != "rediscluster-sample-0" {
			t.Fatalf("change = %+v", change)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no Pod change delivered")
	}

	cancel()
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("channel should close when the context ends")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop")
	}
}

func TestKubeWatchEvents(t *testing.T) {
	b := newKubeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := b.WatchEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Not ours: must be skipped. Then ours: must arrive.
	for _, e := range []*eventsv1.Event{
		event("other", "RedisCluster", "another-cluster", "Ignored", now),
		event("ours", "RedisCluster", testCluster, "Healed", now),
	} {
		if _, err := b.Clientset.EventsV1().Events(testNS).Create(ctx, e, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case ev := <-ch:
		if ev.Reason != "Healed" || ev.Message != "Healed happened" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event delivered")
	}

	cancel()
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("channel should close when the context ends")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop")
	}
}

func TestKubeActions(t *testing.T) {
	b := newKubeFixture(t)
	ctx := context.Background()

	pod := clusterPod("rediscluster-sample-0", testCluster)
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: testCluster, Namespace: testNS}}
	if _, err := b.Clientset.CoreV1().Pods(testNS).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Clientset.AppsV1().StatefulSets(testNS).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := b.DeletePod(ctx, "rediscluster-sample-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Clientset.CoreV1().Pods(testNS).Get(ctx, "rediscluster-sample-0", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the Pod should be gone, err = %v", err)
	}
	if err := b.DeletePod(ctx, "rediscluster-sample-0"); err == nil {
		t.Fatal("deleting a missing Pod must report an error")
	}

	if err := b.DeleteStatefulSet(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Clientset.AppsV1().StatefulSets(testNS).Get(ctx, testCluster, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the StatefulSet should be gone, err = %v", err)
	}
	if err := b.DeleteStatefulSet(ctx); err == nil {
		t.Fatal("deleting a missing StatefulSet must report an error")
	}
}

func TestKubeSetReplicasAndVersionChangeOnlyThatField(t *testing.T) {
	b := newKubeFixture(t)
	ctx := context.Background()

	// A field the portal must not touch.
	rc := &cachev1alpha1.RedisCluster{}
	if err := b.CR.Get(ctx, client.ObjectKey{Namespace: testNS, Name: testCluster}, rc); err != nil {
		t.Fatal(err)
	}
	rc.Spec.Storage.Size = rc.Spec.Storage.Size.DeepCopy()
	rc.Spec.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: *newQuantity(t, "256Mi")}
	if err := b.CR.Update(ctx, rc); err != nil {
		t.Fatal(err)
	}

	if err := b.SetReplicas(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := b.SetVersion(ctx, "7.4"); err != nil {
		t.Fatal(err)
	}

	got := &cachev1alpha1.RedisCluster{}
	if err := b.CR.Get(ctx, client.ObjectKey{Namespace: testNS, Name: testCluster}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Replicas != 5 || got.Spec.Version != "7.4" {
		t.Fatalf("spec = %+v", got.Spec)
	}
	if mem := got.Spec.Resources.Limits.Memory(); mem.String() != "256Mi" {
		t.Fatalf("an unrelated field was changed: memory limit = %s", mem.String())
	}
}

func TestKubeSetOnMissingClusterFails(t *testing.T) {
	b := newKubeFixture(t)
	b.Cluster = "missing"
	if err := b.SetReplicas(context.Background(), 5); err == nil {
		t.Fatal("expected an error for a missing RedisCluster")
	}
	if err := b.SetVersion(context.Background(), "7.4"); err == nil {
		t.Fatal("expected an error for a missing RedisCluster")
	}
}

func TestNewKubeBackend(t *testing.T) {
	// Building a client does not contact the server, so an address nobody
	// listens on is enough to exercise construction.
	b, err := NewKubeBackend(&rest.Config{Host: "http://127.0.0.1:1"}, testNS, testCluster)
	if err != nil {
		t.Fatal(err)
	}
	if b.Namespace != testNS || b.Cluster != testCluster || b.CR == nil || b.Clientset == nil {
		t.Fatalf("unexpected backend: %+v", b)
	}
}

func newQuantity(t *testing.T, s string) *resource.Quantity {
	t.Helper()
	q := resource.MustParse(s)
	return &q
}
