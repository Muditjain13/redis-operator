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

// Package dashboard implements the demo portal backend: a small HTTP API that
// shows one RedisCluster and its Pods live, offers a fixed set of
// failure-injection actions, and measures how long the cluster takes to heal.
package dashboard

import (
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

// PodState is the simplified lifecycle state shown on a Pod card.
type PodState string

// The states a Pod card can be in.
const (
	PodPending     PodState = "Pending"
	PodRunning     PodState = "Running"
	PodReady       PodState = "Ready"
	PodTerminating PodState = "Terminating"
	PodFailed      PodState = "Failed"
)

// PodView is the part of a Pod the portal displays.
type PodView struct {
	Name      string    `json:"name"`
	UID       string    `json:"uid"`
	State     PodState  `json:"state"`
	Image     string    `json:"image"`
	Node      string    `json:"node,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// NewPodView converts a Pod. A Pod that has a deletion timestamp is
// Terminating even if it is still Ready, because that is what a viewer
// needs to see when a Pod is being killed.
func NewPodView(p *corev1.Pod) PodView {
	v := PodView{
		Name:      p.Name,
		UID:       string(p.UID),
		Node:      p.Spec.NodeName,
		CreatedAt: p.CreationTimestamp.Time,
	}
	if len(p.Spec.Containers) > 0 {
		v.Image = p.Spec.Containers[0].Image
	}
	v.State = podState(p)
	return v
}

func podState(p *corev1.Pod) PodState {
	switch {
	case p.DeletionTimestamp != nil:
		return PodTerminating
	case p.Status.Phase == corev1.PodFailed:
		return PodFailed
	case p.Status.Phase == corev1.PodRunning:
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return PodReady
			}
		}
		return PodRunning
	default:
		return PodPending
	}
}

// PodChange is one item of the Pod watch stream.
type PodChange struct {
	Type string  `json:"type"` // ADDED, MODIFIED or DELETED
	Pod  PodView `json:"pod"`
}

// ConditionView is a RedisCluster status condition.
type ConditionView struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// ClusterView is the part of a RedisCluster the portal displays.
type ClusterView struct {
	Name           string          `json:"name"`
	Namespace      string          `json:"namespace"`
	Replicas       int32           `json:"replicas"`
	Version        string          `json:"version"`
	ReadyReplicas  int32           `json:"readyReplicas"`
	CurrentVersion string          `json:"currentVersion"`
	StatefulSetUID string          `json:"statefulSetUID,omitempty"`
	Conditions     []ConditionView `json:"conditions"`
}

// NewClusterView converts a RedisCluster. statefulSetUID is empty when the
// StatefulSet does not exist (for example just after it was deleted).
func NewClusterView(rc *cachev1alpha1.RedisCluster, statefulSetUID string) ClusterView {
	v := ClusterView{
		Name:           rc.Name,
		Namespace:      rc.Namespace,
		Replicas:       rc.Spec.Replicas,
		Version:        rc.Spec.Version,
		ReadyReplicas:  rc.Status.ReadyReplicas,
		CurrentVersion: rc.Status.CurrentVersion,
		StatefulSetUID: statefulSetUID,
		Conditions:     []ConditionView{},
	}
	for _, c := range rc.Status.Conditions {
		v.Conditions = append(v.Conditions, ConditionView{
			Type: c.Type, Status: string(c.Status), Reason: c.Reason, Message: c.Message,
		})
	}
	return v
}

// ConditionStatus returns the status ("True", "False") of a condition, or ""
// if the operator has not reported it yet.
func (c ClusterView) ConditionStatus(condType string) string {
	for _, cond := range c.Conditions {
		if cond.Type == condType {
			return cond.Status
		}
	}
	return ""
}

// EventView is an operator event about the RedisCluster.
type EventView struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // Normal or Warning
	Reason  string    `json:"reason"`
	Message string    `json:"message"`
}

// HealRecord says how long one demo action took to settle.
type HealRecord struct {
	Action     Action    `json:"action"`
	Seconds    float64   `json:"seconds"`
	FinishedAt time.Time `json:"finishedAt"`
	// TimedOut is true if the cluster did not settle in time; Seconds is then
	// the time waited, not a heal time.
	TimedOut bool `json:"timedOut,omitempty"`
}

// Operation is a demo action that is still settling.
type Operation struct {
	Action    Action    `json:"action"`
	StartedAt time.Time `json:"startedAt"`
}

// State is everything the portal shows, sent as one snapshot.
type State struct {
	Cluster  *ClusterView `json:"cluster"`
	Pods     []PodView    `json:"pods"`
	Events   []EventView  `json:"events"`
	Heals    []HealRecord `json:"heals"`
	InFlight *Operation   `json:"inFlight"`
}

// sortPods orders Pods by name, which for a StatefulSet is by ordinal. A plain
// string sort is enough for the 1 to 7 replicas the CRD allows (X-10 cannot occur).
func sortPods(pods []PodView) {
	slices.SortFunc(pods, func(a, b PodView) int { return strings.Compare(a.Name, b.Name) })
}

// imageTag returns the tag of a "repo:tag" image reference.
func imageTag(image string) string {
	if i := strings.LastIndex(image, ":"); i >= 0 {
		return image[i+1:]
	}
	return ""
}
