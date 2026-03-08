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
	"fmt"
	"slices"
	"time"

	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

// KubeBackend is the Backend that talks to a real (or fake) API server.
type KubeBackend struct {
	// CR reads and patches the RedisCluster.
	CR client.Client
	// Clientset reads and changes Pods, the StatefulSet and events.
	Clientset kubernetes.Interface

	Namespace string
	Cluster   string
}

var _ Backend = (*KubeBackend)(nil)

// NewKubeBackend builds a KubeBackend from a rest config.
func NewKubeBackend(cfg *rest.Config, namespace, cluster string) (*KubeBackend, error) {
	scheme := runtime.NewScheme()
	if err := cachev1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering RedisCluster types: %w", err)
	}
	cr, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating RedisCluster client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}
	return &KubeBackend{CR: cr, Clientset: cs, Namespace: namespace, Cluster: cluster}, nil
}

func (b *KubeBackend) podSelector() string {
	return labels.SelectorFromSet(labels.Set{
		"app.kubernetes.io/name":     "redis",
		"app.kubernetes.io/instance": b.Cluster,
	}).String()
}

func (b *KubeBackend) key() client.ObjectKey {
	return client.ObjectKey{Namespace: b.Namespace, Name: b.Cluster}
}

// GetCluster implements Backend.
func (b *KubeBackend) GetCluster(ctx context.Context) (*ClusterView, error) {
	rc := &cachev1alpha1.RedisCluster{}
	if err := b.CR.Get(ctx, b.key(), rc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrClusterNotFound
		}
		return nil, fmt.Errorf("getting RedisCluster: %w", err)
	}

	var stsUID string
	sts, err := b.Clientset.AppsV1().StatefulSets(b.Namespace).Get(ctx, b.Cluster, metav1.GetOptions{})
	switch {
	case err == nil:
		stsUID = string(sts.UID)
	case apierrors.IsNotFound(err):
		// The StatefulSet is missing right now, e.g. during the delete demo.
	default:
		return nil, fmt.Errorf("getting StatefulSet: %w", err)
	}

	view := NewClusterView(rc, stsUID)
	return &view, nil
}

// ListPods implements Backend.
func (b *KubeBackend) ListPods(ctx context.Context) ([]PodView, error) {
	list, err := b.Clientset.CoreV1().Pods(b.Namespace).List(ctx, metav1.ListOptions{LabelSelector: b.podSelector()})
	if err != nil {
		return nil, fmt.Errorf("listing Pods: %w", err)
	}
	pods := make([]PodView, 0, len(list.Items))
	for i := range list.Items {
		pods = append(pods, NewPodView(&list.Items[i]))
	}
	sortPods(pods)
	return pods, nil
}

// ListEvents implements Backend.
func (b *KubeBackend) ListEvents(ctx context.Context, limit int) ([]EventView, error) {
	list, err := b.Clientset.EventsV1().Events(b.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	var out []EventView
	for i := range list.Items {
		if b.isOurs(&list.Items[i]) {
			out = append(out, newEventView(&list.Items[i]))
		}
	}
	slices.SortStableFunc(out, func(a, b EventView) int { return a.Time.Compare(b.Time) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// WatchPods implements Backend.
func (b *KubeBackend) WatchPods(ctx context.Context) (<-chan PodChange, error) {
	w, err := b.Clientset.CoreV1().Pods(b.Namespace).Watch(ctx, metav1.ListOptions{LabelSelector: b.podSelector()})
	if err != nil {
		return nil, fmt.Errorf("watching Pods: %w", err)
	}
	out := make(chan PodChange)
	go func() {
		defer close(out)
		defer w.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.ResultChan():
				if !ok {
					return
				}
				pod, isPod := ev.Object.(*corev1.Pod)
				if !isPod || ev.Type == watch.Error || ev.Type == watch.Bookmark {
					continue
				}
				select {
				case out <- PodChange{Type: string(ev.Type), Pod: NewPodView(pod)}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// WatchEvents implements Backend.
func (b *KubeBackend) WatchEvents(ctx context.Context) (<-chan EventView, error) {
	w, err := b.Clientset.EventsV1().Events(b.Namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("watching events: %w", err)
	}
	out := make(chan EventView)
	go func() {
		defer close(out)
		defer w.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.ResultChan():
				if !ok {
					return
				}
				e, isEvent := ev.Object.(*eventsv1.Event)
				if !isEvent || ev.Type == watch.Deleted || !b.isOurs(e) {
					continue
				}
				select {
				case out <- newEventView(e):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// DeletePod implements Backend.
func (b *KubeBackend) DeletePod(ctx context.Context, name string) error {
	if err := b.Clientset.CoreV1().Pods(b.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("deleting Pod %s: %w", name, err)
	}
	return nil
}

// DeleteStatefulSet implements Backend.
func (b *KubeBackend) DeleteStatefulSet(ctx context.Context) error {
	if err := b.Clientset.AppsV1().StatefulSets(b.Namespace).Delete(ctx, b.Cluster, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("deleting StatefulSet: %w", err)
	}
	return nil
}

// SetReplicas implements Backend.
func (b *KubeBackend) SetReplicas(ctx context.Context, replicas int32) error {
	return b.patchSpec(ctx, func(s *cachev1alpha1.RedisClusterSpec) { s.Replicas = replicas })
}

// SetVersion implements Backend.
func (b *KubeBackend) SetVersion(ctx context.Context, version string) error {
	return b.patchSpec(ctx, func(s *cachev1alpha1.RedisClusterSpec) { s.Version = version })
}

// patchSpec changes one field of the spec with a merge patch, so nothing else
// the user may have set is touched.
func (b *KubeBackend) patchSpec(ctx context.Context, change func(*cachev1alpha1.RedisClusterSpec)) error {
	rc := &cachev1alpha1.RedisCluster{}
	if err := b.CR.Get(ctx, b.key(), rc); err != nil {
		return fmt.Errorf("getting RedisCluster: %w", err)
	}
	orig := rc.DeepCopy()
	change(&rc.Spec)
	if err := b.CR.Patch(ctx, rc, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("patching RedisCluster: %w", err)
	}
	return nil
}

// ResetSpec puts replicas and version back to the given values with one merge
// patch. It reports whether anything had to change. It touches nothing but the
// RedisCluster, so the CronJob that calls it needs no other permission.
func (b *KubeBackend) ResetSpec(ctx context.Context, replicas int32, version string) (bool, error) {
	rc := &cachev1alpha1.RedisCluster{}
	if err := b.CR.Get(ctx, b.key(), rc); err != nil {
		if apierrors.IsNotFound(err) {
			return false, ErrClusterNotFound
		}
		return false, fmt.Errorf("getting RedisCluster: %w", err)
	}
	if rc.Spec.Replicas == replicas && rc.Spec.Version == version {
		return false, nil
	}
	orig := rc.DeepCopy()
	rc.Spec.Replicas = replicas
	rc.Spec.Version = version
	if err := b.CR.Patch(ctx, rc, client.MergeFrom(orig)); err != nil {
		return false, fmt.Errorf("patching RedisCluster: %w", err)
	}
	return true, nil
}

// isOurs reports whether the event is about this RedisCluster.
func (b *KubeBackend) isOurs(e *eventsv1.Event) bool {
	return e.Regarding.Kind == "RedisCluster" && e.Regarding.Name == b.Cluster
}

func newEventView(e *eventsv1.Event) EventView {
	return EventView{Time: eventTime(e), Type: e.Type, Reason: e.Reason, Message: e.Note}
}

// eventTime picks the most meaningful timestamp an events.k8s.io event carries.
func eventTime(e *eventsv1.Event) time.Time {
	switch {
	case e.Series != nil && !e.Series.LastObservedTime.IsZero():
		return e.Series.LastObservedTime.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	case !e.DeprecatedLastTimestamp.IsZero():
		return e.DeprecatedLastTimestamp.Time
	default:
		return e.CreationTimestamp.Time
	}
}
