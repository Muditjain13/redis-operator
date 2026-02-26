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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

func TestNewPodViewState(t *testing.T) {
	now := metav1.Now()
	ready := []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	notReady := []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}

	tests := []struct {
		name string
		pod  corev1.Pod
		want PodState
	}{
		{"pending", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}, PodPending},
		{"no phase yet", corev1.Pod{}, PodPending},
		{"running not ready", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: notReady}}, PodRunning},
		{"running no conditions", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}, PodRunning},
		{"ready", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: ready}}, PodReady},
		{"failed", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}}, PodFailed},
		{
			"terminating wins even when ready",
			corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning, Conditions: ready},
			},
			PodTerminating,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewPodView(&tt.pod).State; got != tt.want {
				t.Fatalf("state = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNewPodViewFields(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "x-0", UID: "u1", CreationTimestamp: metav1.NewTime(created)},
		Spec:       corev1.PodSpec{NodeName: "node-a", Containers: []corev1.Container{{Image: "redis:7.2"}}},
	}
	got := NewPodView(&pod)
	if got.Name != "x-0" || got.UID != "u1" || got.Node != "node-a" || got.Image != "redis:7.2" || !got.CreatedAt.Equal(created) {
		t.Fatalf("unexpected view: %+v", got)
	}
	if v := NewPodView(&corev1.Pod{}); v.Image != "" {
		t.Fatalf("pod without containers should have no image, got %q", v.Image)
	}
}

func TestNewClusterViewAndConditionStatus(t *testing.T) {
	rc := &cachev1alpha1.RedisCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "rc", Namespace: "demo"},
		Spec:       cachev1alpha1.RedisClusterSpec{Replicas: 3, Version: "7.2"},
		Status: cachev1alpha1.RedisClusterStatus{
			ReadyReplicas: 2, CurrentVersion: "7.2",
			Conditions: []metav1.Condition{
				{Type: "Progressing", Status: metav1.ConditionTrue, Reason: "RolloutInProgress", Message: "2/3 ready"},
			},
		},
	}
	v := NewClusterView(rc, "sts-uid")
	if v.Name != "rc" || v.Replicas != 3 || v.ReadyReplicas != 2 || v.StatefulSetUID != "sts-uid" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if got := v.ConditionStatus("Progressing"); got != "True" {
		t.Fatalf("Progressing = %q, want True", got)
	}
	if got := v.ConditionStatus("Available"); got != "" {
		t.Fatalf("an unreported condition should be empty, got %q", got)
	}
	if empty := NewClusterView(&cachev1alpha1.RedisCluster{}, ""); empty.Conditions == nil {
		t.Fatal("conditions must encode as [] not null")
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{"redis:7.4": "7.4", "registry:5000/redis:7.2": "7.2", "redis": ""} {
		if got := imageTag(in); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortPodsByName(t *testing.T) {
	pods := []PodView{{Name: "x-2"}, {Name: "x-0"}, {Name: "x-1"}}
	sortPods(pods)
	if pods[0].Name != "x-0" || pods[1].Name != "x-1" || pods[2].Name != "x-2" {
		t.Fatalf("not sorted: %+v", pods)
	}
}
