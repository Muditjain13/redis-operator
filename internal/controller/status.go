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

package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

// Condition reasons.
const (
	reasonReplicasReady     = "ReplicasReady"
	reasonNoReplicasReady   = "NoReplicasReady"
	reasonRolloutInProgress = "RolloutInProgress"
	reasonUpgradeInProgress = "UpgradeInProgress"
	reasonRolloutComplete   = "RolloutComplete"
	reasonReconcileFailed   = "ReconcileFailed"
	reasonReconcileOK       = "ReconcileSucceeded"
)

// rolloutComplete reports whether the StatefulSet has fully converged on the
// desired replica count and pod template, with every pod ready.
//
// The generation check matters: right after the operator updates the
// StatefulSet, its status still describes the previous spec until the
// StatefulSet controller observes the change. Without it we would briefly
// report a finished rollout for a change that has not started.
func rolloutComplete(rc *cachev1alpha1.RedisCluster, sts *appsv1.StatefulSet) bool {
	desired := rc.Spec.Replicas
	s := sts.Status
	return s.ObservedGeneration == sts.Generation &&
		s.Replicas == desired &&
		s.UpdatedReplicas == desired &&
		s.ReadyReplicas == desired &&
		s.CurrentRevision == s.UpdateRevision
}

// computeStatus fills rc.Status from the observed StatefulSet and the outcome
// of this reconcile. sts is nil when the owned resources could not be applied,
// in which case only the Degraded condition (and nothing observed) changes.
func computeStatus(rc *cachev1alpha1.RedisCluster, sts *appsv1.StatefulSet, reconcileErr error) {
	st := &rc.Status

	if sts != nil {
		complete := rolloutComplete(rc, sts)

		st.ObservedGeneration = rc.Generation
		st.ReadyReplicas = sts.Status.ReadyReplicas
		if complete {
			// currentVersion only advances once every replica runs the new
			// version, so it lags spec.version during a rolling upgrade.
			st.CurrentVersion = rc.Spec.Version
		}

		setCondition(rc, cachev1alpha1.ConditionAvailable, availability(rc, sts))
		setCondition(rc, cachev1alpha1.ConditionProgressing, progress(rc, sts, complete))
	}

	degraded := condition{status: metav1.ConditionFalse, reason: reasonReconcileOK, message: "Owned resources are in sync"}
	if reconcileErr != nil {
		degraded = condition{status: metav1.ConditionTrue, reason: reasonReconcileFailed, message: reconcileErr.Error()}
	}
	setCondition(rc, cachev1alpha1.ConditionDegraded, degraded)
}

type condition struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

// Available means the cluster can serve traffic: at least one replica is ready.
func availability(rc *cachev1alpha1.RedisCluster, sts *appsv1.StatefulSet) condition {
	ready := sts.Status.ReadyReplicas
	msg := fmt.Sprintf("%d/%d replicas ready", ready, rc.Spec.Replicas)
	if ready == 0 {
		return condition{metav1.ConditionFalse, reasonNoReplicasReady, msg}
	}
	return condition{metav1.ConditionTrue, reasonReplicasReady, msg}
}

// Progressing means the cluster is still being created, scaled or upgraded.
func progress(rc *cachev1alpha1.RedisCluster, sts *appsv1.StatefulSet, complete bool) condition {
	if complete {
		return condition{metav1.ConditionFalse, reasonRolloutComplete, "All replicas are up to date and ready"}
	}
	msg := fmt.Sprintf("%d/%d replicas updated, %d/%d ready",
		sts.Status.UpdatedReplicas, rc.Spec.Replicas, sts.Status.ReadyReplicas, rc.Spec.Replicas)

	// currentVersion still holds the old version until the rollout finishes,
	// so a mismatch with the spec identifies a rolling upgrade.
	if from := rc.Status.CurrentVersion; from != "" && from != rc.Spec.Version {
		return condition{metav1.ConditionTrue, reasonUpgradeInProgress,
			fmt.Sprintf("Upgrading Redis from %s to %s: %s", from, rc.Spec.Version, msg)}
	}
	return condition{metav1.ConditionTrue, reasonRolloutInProgress, msg}
}

func setCondition(rc *cachev1alpha1.RedisCluster, condType string, c condition) {
	// SetStatusCondition only bumps LastTransitionTime when the status changes,
	// so repeated reconciles produce an identical object and no write.
	meta.SetStatusCondition(&rc.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             c.status,
		Reason:             c.reason,
		Message:            c.message,
		ObservedGeneration: rc.Generation,
	})
}
