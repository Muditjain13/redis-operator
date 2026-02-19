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
	"context"
	"errors"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

// finalizerName blocks deletion of a RedisCluster until the operator has run
// its cleanup.
const finalizerName = "cache.mudit.dev/finalizer"

// Event reasons.
const (
	eventCreated   = "Created"
	eventScaled    = "Scaled"
	eventUpgrading = "Upgrading"
	eventUpgraded  = "Upgraded"
	eventHealed    = "Healed"
)

// RedisClusterReconciler reconciles a RedisCluster object
type RedisClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=cache.mudit.dev,resources=redisclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.mudit.dev,resources=redisclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cache.mudit.dev,resources=redisclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile drives the owned ConfigMap, headless Service and StatefulSet
// towards the state described by the RedisCluster spec, then reports what it
// observed in the RedisCluster status.
//
// It is level-based and idempotent: it never asks what changed, it compares
// desired and actual state, so a deleted child, a hand-edited child and a spec
// change are all handled by the same code path. Returning an error makes
// controller-runtime requeue the request with exponential backoff.
func (r *RedisClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	rc := &cachev1alpha1.RedisCluster{}
	if err := r.Get(ctx, req.NamespacedName, rc); err != nil {
		// Deleted before we got here; owned objects are garbage collected.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rc.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.reconcileDelete(ctx, rc)
	}

	if controllerutil.AddFinalizer(rc, finalizerName) {
		if err := r.Update(ctx, rc); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Added finalizer", "finalizer", finalizerName)
	}

	sts, err := r.reconcileResources(ctx, rc)
	// Status is written even when reconciling failed, so Degraded is visible.
	statusErr := r.updateStatus(ctx, rc, sts, err)
	return ctrl.Result{}, errors.Join(err, statusErr)
}

// reconcileResources applies the ConfigMap, Service and StatefulSet and
// returns the StatefulSet as last seen by the API server.
func (r *RedisClusterReconciler) reconcileResources(
	ctx context.Context,
	rc *cachev1alpha1.RedisCluster,
) (*appsv1.StatefulSet, error) {
	cm := &corev1.ConfigMap{}
	cm.Name, cm.Namespace = configMapName(rc), rc.Namespace
	if _, err := r.apply(ctx, rc, cm, func() { mutateConfigMap(cm, rc) }); err != nil {
		return nil, err
	}

	svc := &corev1.Service{}
	svc.Name, svc.Namespace = rc.Name, rc.Namespace
	if _, err := r.apply(ctx, rc, svc, func() { mutateService(svc, rc) }); err != nil {
		return nil, err
	}

	sts := &appsv1.StatefulSet{}
	sts.Name, sts.Namespace = rc.Name, rc.Namespace
	var before stsSnapshot
	op, err := r.apply(ctx, rc, sts, func() {
		before = snapshot(sts) // taken before mutating, so it shows the live state
		mutateStatefulSet(sts, rc)
	})
	if err != nil {
		return nil, err
	}
	if op == controllerutil.OperationResultUpdated && !alreadyReconciled(rc) {
		r.recordSpecChange(rc, sts, before)
	}
	return sts, nil
}

// apply creates obj or updates it in place so that it matches mutate, makes rc
// its controlling owner so Kubernetes garbage-collects it with rc, and records
// an event describing what it had to do.
func (r *RedisClusterReconciler) apply(
	ctx context.Context,
	rc *cachev1alpha1.RedisCluster,
	obj client.Object,
	mutate func(),
) (controllerutil.OperationResult, error) {
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
		mutate()
		return controllerutil.SetControllerReference(rc, obj, r.Scheme)
	})
	if err != nil || op == controllerutil.OperationResultNone {
		return op, err
	}

	gvk, _ := apiutil.GVKForObject(obj, r.Scheme)
	logf.FromContext(ctx).Info("Reconciled owned resource",
		"kind", gvk.Kind, "name", obj.GetName(), "operation", op)
	r.recordApply(rc, obj, gvk.Kind, op)
	return op, nil
}

// alreadyReconciled reports whether the controller has already processed the
// current generation of the spec. From then on, any change it has to make to an
// owned resource was not requested by the user.
func alreadyReconciled(rc *cachev1alpha1.RedisCluster) bool {
	return rc.Status.ObservedGeneration == rc.Generation
}

// recordApply emits an event for a change made to an owned resource.
//
// If the controller already reconciled this generation of the spec, nobody
// asked for a change, so a create or update means something outside the
// operator deleted or modified the resource and we put it back: that is
// self-healing. Otherwise the change follows from a spec edit.
func (r *RedisClusterReconciler) recordApply(
	rc *cachev1alpha1.RedisCluster,
	obj client.Object,
	kind string,
	op controllerutil.OperationResult,
) {
	healing := alreadyReconciled(rc)

	switch {
	case healing && op == controllerutil.OperationResultCreated:
		r.Recorder.Eventf(rc, obj, corev1.EventTypeNormal, eventHealed, "Recreate",
			"Recreated missing %s %s", kind, obj.GetName())
	case healing && op == controllerutil.OperationResultUpdated:
		r.Recorder.Eventf(rc, obj, corev1.EventTypeNormal, eventHealed, "Restore",
			"Restored %s %s to match the spec", kind, obj.GetName())
	case op == controllerutil.OperationResultCreated:
		r.Recorder.Eventf(rc, obj, corev1.EventTypeNormal, eventCreated, "Create",
			"Created %s %s", kind, obj.GetName())
	}
}

// stsSnapshot is the part of a StatefulSet that a spec change can alter.
type stsSnapshot struct {
	exists   bool
	replicas int32
	version  string
}

func snapshot(sts *appsv1.StatefulSet) stsSnapshot {
	s := stsSnapshot{exists: !sts.CreationTimestamp.IsZero(), replicas: ptr.Deref(sts.Spec.Replicas, 0)}
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == redisContainerName {
			s.version = strings.TrimPrefix(c.Image, redisImageRepo+":")
		}
	}
	return s
}

// recordSpecChange emits Scaled and Upgrading events for the StatefulSet
// change that a spec edit caused. The matching Upgraded event is emitted by
// updateStatus once every replica runs the new version.
func (r *RedisClusterReconciler) recordSpecChange(rc *cachev1alpha1.RedisCluster, sts *appsv1.StatefulSet, before stsSnapshot) {
	if !before.exists {
		return
	}
	if after := ptr.Deref(sts.Spec.Replicas, 0); after != before.replicas {
		r.Recorder.Eventf(rc, sts, corev1.EventTypeNormal, eventScaled, "Scale",
			"Scaled from %d to %d replicas", before.replicas, after)
	}
	if rc.Spec.Version != before.version {
		r.Recorder.Eventf(rc, sts, corev1.EventTypeNormal, eventUpgrading, "Upgrade",
			"Rolling upgrade started from Redis %s to %s", before.version, rc.Spec.Version)
	}
}

// updateStatus recomputes rc.Status and writes it only if it changed. Skipping
// no-op writes matters: a status update is itself a watch event for rc, so
// unconditional writes would make the controller reconcile itself forever.
func (r *RedisClusterReconciler) updateStatus(
	ctx context.Context,
	rc *cachev1alpha1.RedisCluster,
	sts *appsv1.StatefulSet,
	reconcileErr error,
) error {
	orig := rc.DeepCopy()
	computeStatus(rc, sts, reconcileErr)
	if equality.Semantic.DeepEqual(orig.Status, rc.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, rc, client.MergeFrom(orig)); err != nil {
		return err
	}
	if from, to := orig.Status.CurrentVersion, rc.Status.CurrentVersion; from != "" && from != to {
		r.Recorder.Eventf(rc, nil, corev1.EventTypeNormal, eventUpgraded, "Upgrade",
			"Rolling upgrade finished: all replicas run Redis %s", to)
	}
	return nil
}

// reconcileDelete runs cleanup and releases the finalizer. Owned resources
// need no explicit deletion: their owner references make the garbage collector
// remove them once the RedisCluster is gone.
func (r *RedisClusterReconciler) reconcileDelete(ctx context.Context, rc *cachev1alpha1.RedisCluster) error {
	if !controllerutil.ContainsFinalizer(rc, finalizerName) {
		return nil
	}
	logf.FromContext(ctx).Info("Cleaning up RedisCluster", "name", rc.Name)
	controllerutil.RemoveFinalizer(rc, finalizerName)
	return r.Update(ctx, rc)
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedisClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cachev1alpha1.RedisCluster{}).
		// Owns maps events on owned objects back to their RedisCluster, so a
		// deleted or edited child triggers a reconcile of its owner.
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Named("rediscluster").
		Complete(r)
}
