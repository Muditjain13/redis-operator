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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

var _ = Describe("RedisCluster Controller", func() {
	const namespace = "default"

	var (
		ctx        context.Context
		reconciler *RedisClusterReconciler
		recorder   *events.FakeRecorder
		key        types.NamespacedName
	)

	// newCluster creates a RedisCluster with a name unique to the current spec.
	newCluster := func(mutate func(*cachev1alpha1.RedisCluster)) *cachev1alpha1.RedisCluster {
		rc := &cachev1alpha1.RedisCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rc-" + rand.String(6),
				Namespace: namespace,
			},
		}
		if mutate != nil {
			mutate(rc)
		}
		Expect(k8sClient.Create(ctx, rc)).To(Succeed())
		key = client.ObjectKeyFromObject(rc)
		DeferCleanup(func() {
			// Drop the finalizer so the object can actually be deleted.
			got := &cachev1alpha1.RedisCluster{}
			if err := k8sClient.Get(ctx, key, got); err == nil {
				got.Finalizers = nil
				Expect(k8sClient.Update(ctx, got)).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, got))).To(Succeed())
			}
		})
		return rc
	}

	reconcileOnce := func() {
		GinkgoHelper()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
	}

	// drainEvents returns and clears the events recorded so far.
	drainEvents := func() []string {
		var got []string
		for {
			select {
			case e := <-recorder.Events:
				got = append(got, e)
			default:
				return got
			}
		}
	}

	// markStatefulSetStatus plays the role of the StatefulSet controller, which
	// does not run in envtest (there is no kube-controller-manager or kubelet).
	markStatefulSetStatus := func(update func(*appsv1.StatefulSetStatus, *appsv1.StatefulSet)) {
		GinkgoHelper()
		sts := &appsv1.StatefulSet{}
		Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
		update(&sts.Status, sts)
		Expect(k8sClient.Status().Update(ctx, sts)).To(Succeed())
	}

	get := func() *cachev1alpha1.RedisCluster {
		GinkgoHelper()
		got := &cachev1alpha1.RedisCluster{}
		Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
		return got
	}

	conditionOf := func(rc *cachev1alpha1.RedisCluster, t string) *metav1.Condition {
		GinkgoHelper()
		c := meta.FindStatusCondition(rc.Status.Conditions, t)
		Expect(c).NotTo(BeNil(), "condition %s missing", t)
		return c
	}

	// editSpec changes the spec the way a user running kubectl edit would.
	editSpec := func(edit func(*cachev1alpha1.RedisClusterSpec)) {
		GinkgoHelper()
		got := get()
		edit(&got.Spec)
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
	}

	// rolloutProgress reports the StatefulSet status as the StatefulSet
	// controller would after observing the latest spec.
	rolloutProgress := func(replicas, updated, ready int32, current, update string) {
		GinkgoHelper()
		markStatefulSetStatus(func(s *appsv1.StatefulSetStatus, sts *appsv1.StatefulSet) {
			s.ObservedGeneration = sts.Generation
			s.Replicas, s.UpdatedReplicas, s.ReadyReplicas = replicas, updated, ready
			s.CurrentRevision, s.UpdateRevision = current, update
		})
	}

	BeforeEach(func() {
		ctx = context.Background()
		recorder = events.NewFakeRecorder(100)
		reconciler = &RedisClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder}
	})

	Context("creating a RedisCluster", func() {
		var rc *cachev1alpha1.RedisCluster

		BeforeEach(func() {
			rc = newCluster(func(rc *cachev1alpha1.RedisCluster) {
				rc.Spec.Replicas = 3
				rc.Spec.Version = "7.4"
				rc.Spec.Storage.Size = resource.MustParse("2Gi")
				rc.Spec.Resources = corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				}
			})
			reconcileOnce()
		})

		It("creates a StatefulSet matching the spec", func() {
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())

			Expect(*sts.Spec.Replicas).To(Equal(int32(3)))
			Expect(sts.Spec.ServiceName).To(Equal(rc.Name))
			Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
			Expect(sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests.Storage().String()).To(Equal("2Gi"))

			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			c := sts.Spec.Template.Spec.Containers[0]
			Expect(c.Image).To(Equal("redis:7.4"))
			Expect(c.Resources.Requests.Cpu().String()).To(Equal("100m"))
		})

		It("adds liveness and readiness probes that run redis-cli ping", func() {
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())

			c := sts.Spec.Template.Spec.Containers[0]
			for _, probe := range []*corev1.Probe{c.LivenessProbe, c.ReadinessProbe} {
				Expect(probe).NotTo(BeNil())
				Expect(probe.Exec.Command[len(probe.Exec.Command)-1]).To(ContainSubstring("redis-cli ping"))
			}
		})

		It("creates a headless Service selecting the pods", func() {
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())

			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Selector).To(Equal(selectorLabels(rc)))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(redisPort)))
		})

		It("creates a ConfigMap with the Redis configuration", func() {
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: configMapName(rc)}, cm)).To(Succeed())
			Expect(cm.Data).To(HaveKeyWithValue(configKey, redisConfig))
		})

		It("sets the RedisCluster as controller owner of every child", func() {
			children := []client.Object{&appsv1.StatefulSet{}, &corev1.Service{}, &corev1.ConfigMap{}}
			names := []string{rc.Name, rc.Name, configMapName(rc)}
			for i, child := range children {
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: names[i]}, child)).To(Succeed())
				owner := metav1.GetControllerOf(child)
				Expect(owner).NotTo(BeNil())
				Expect(owner.Kind).To(Equal("RedisCluster"))
				Expect(owner.Name).To(Equal(rc.Name))
			}
		})

		It("adds the finalizer", func() {
			got := &cachev1alpha1.RedisCluster{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement(finalizerName))
		})

		It("is idempotent: a second reconcile changes nothing", func() {
			before := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, before)).To(Succeed())
			beforeSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, key, beforeSvc)).To(Succeed())

			reconcileOnce()

			after := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, after)).To(Succeed())
			afterSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, key, afterSvc)).To(Succeed())
			Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
			Expect(afterSvc.ResourceVersion).To(Equal(beforeSvc.ResourceVersion))
		})
	})

	Context("applying CRD defaults", func() {
		It("defaults replicas, version and storage size", func() {
			newCluster(nil)
			got := &cachev1alpha1.RedisCluster{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())

			Expect(got.Spec.Replicas).To(Equal(int32(1)))
			Expect(got.Spec.Version).To(Equal("7.2"))
			Expect(got.Spec.Storage.Size.String()).To(Equal("1Gi"))
		})

		It("rejects replicas outside 1-7", func() {
			for _, replicas := range []int32{-1, 8} {
				rc := &cachev1alpha1.RedisCluster{
					ObjectMeta: metav1.ObjectMeta{GenerateName: "invalid-", Namespace: namespace},
					Spec:       cachev1alpha1.RedisClusterSpec{Replicas: replicas},
				}
				err := k8sClient.Create(ctx, rc)
				Expect(apierrors.IsInvalid(err)).To(BeTrue(), "replicas=%d should be rejected", replicas)
			}
		})
	})

	Context("self-healing", func() {
		var rc *cachev1alpha1.RedisCluster

		BeforeEach(func() {
			rc = newCluster(func(rc *cachev1alpha1.RedisCluster) { rc.Spec.Replicas = 2 })
			reconcileOnce()
			Expect(drainEvents()).To(HaveLen(3), "one Created event per owned resource")
		})

		DescribeTable("recreates a deleted child",
			func(child client.Object, name func() string, kind string) {
				objKey := types.NamespacedName{Namespace: namespace, Name: name()}
				Expect(k8sClient.Get(ctx, objKey, child)).To(Succeed())
				Expect(k8sClient.Delete(ctx, child)).To(Succeed())
				Expect(apierrors.IsNotFound(k8sClient.Get(ctx, objKey, child))).To(BeTrue())

				reconcileOnce()

				Expect(k8sClient.Get(ctx, objKey, child)).To(Succeed())
				Expect(drainEvents()).To(ConsistOf("Normal Healed Recreated missing " + kind + " " + objKey.Name))
			},
			Entry("StatefulSet", &appsv1.StatefulSet{}, func() string { return key.Name }, "StatefulSet"),
			Entry("Service", &corev1.Service{}, func() string { return key.Name }, "Service"),
			Entry("ConfigMap", &corev1.ConfigMap{}, func() string { return key.Name + "-config" }, "ConfigMap"),
		)

		It("reverts a StatefulSet edited behind the operator's back", func() {
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			sts.Spec.Replicas = ptr.To(int32(6))
			sts.Spec.Template.Spec.Containers[0].Image = "redis:6.0"
			Expect(k8sClient.Update(ctx, sts)).To(Succeed())

			reconcileOnce()

			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			Expect(*sts.Spec.Replicas).To(Equal(int32(2)))
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("redis:7.2"))
			Expect(drainEvents()).To(ConsistOf("Normal Healed Restored StatefulSet " + key.Name + " to match the spec"))
		})

		It("reverts a ConfigMap edited behind the operator's back", func() {
			cm := &corev1.ConfigMap{}
			cmKey := types.NamespacedName{Namespace: namespace, Name: configMapName(rc)}
			Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
			cm.Data[configKey] = "appendonly no\n"
			Expect(k8sClient.Update(ctx, cm)).To(Succeed())

			reconcileOnce()

			Expect(k8sClient.Get(ctx, cmKey, cm)).To(Succeed())
			Expect(cm.Data[configKey]).To(Equal(redisConfig))
		})

		It("does not report a spec change as healing", func() {
			got := &cachev1alpha1.RedisCluster{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			got.Spec.Replicas = 4
			Expect(k8sClient.Update(ctx, got)).To(Succeed())

			reconcileOnce()

			Expect(drainEvents()).NotTo(ContainElement(ContainSubstring("Healed")))
		})
	})

	Context("status", func() {
		var rc *cachev1alpha1.RedisCluster

		BeforeEach(func() {
			rc = newCluster(func(rc *cachev1alpha1.RedisCluster) { rc.Spec.Replicas = 2 })
			reconcileOnce()
		})

		It("reports a new cluster as progressing and not yet available", func() {
			got := get()
			Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
			Expect(got.Status.ReadyReplicas).To(BeZero())
			Expect(got.Status.CurrentVersion).To(BeEmpty())

			Expect(conditionOf(got, cachev1alpha1.ConditionAvailable).Status).To(Equal(metav1.ConditionFalse))
			Expect(conditionOf(got, cachev1alpha1.ConditionProgressing).Status).To(Equal(metav1.ConditionTrue))
			Expect(conditionOf(got, cachev1alpha1.ConditionDegraded).Status).To(Equal(metav1.ConditionFalse))
		})

		It("reports available but still progressing while only some replicas are ready", func() {
			markStatefulSetStatus(func(s *appsv1.StatefulSetStatus, sts *appsv1.StatefulSet) {
				s.ObservedGeneration = sts.Generation
				s.Replicas, s.UpdatedReplicas, s.ReadyReplicas = 2, 2, 1
				s.CurrentRevision, s.UpdateRevision = "rev-1", "rev-1"
			})
			reconcileOnce()

			got := get()
			Expect(got.Status.ReadyReplicas).To(Equal(int32(1)))
			Expect(conditionOf(got, cachev1alpha1.ConditionAvailable).Status).To(Equal(metav1.ConditionTrue))
			Expect(conditionOf(got, cachev1alpha1.ConditionProgressing).Status).To(Equal(metav1.ConditionTrue))
		})

		It("reports available and done once every replica is ready", func() {
			markStatefulSetStatus(func(s *appsv1.StatefulSetStatus, sts *appsv1.StatefulSet) {
				s.ObservedGeneration = sts.Generation
				s.Replicas, s.UpdatedReplicas, s.ReadyReplicas = 2, 2, 2
				s.CurrentRevision, s.UpdateRevision = "rev-1", "rev-1"
			})
			reconcileOnce()

			got := get()
			Expect(got.Status.ReadyReplicas).To(Equal(int32(2)))
			Expect(got.Status.CurrentVersion).To(Equal("7.2"))
			Expect(conditionOf(got, cachev1alpha1.ConditionAvailable).Status).To(Equal(metav1.ConditionTrue))
			Expect(conditionOf(got, cachev1alpha1.ConditionProgressing).Status).To(Equal(metav1.ConditionFalse))
			Expect(conditionOf(got, cachev1alpha1.ConditionDegraded).Status).To(Equal(metav1.ConditionFalse))
		})

		It("does not write status again when nothing changed", func() {
			before := get()
			reconcileOnce()
			Expect(get().ResourceVersion).To(Equal(before.ResourceVersion))
		})

		It("sets Degraded when owned resources cannot be updated, and clears it on recovery", func() {
			got := get()
			got.Spec.Replicas = 3
			Expect(k8sClient.Update(ctx, got)).To(Succeed())

			watchClient, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
			Expect(err).NotTo(HaveOccurred())
			failing := &RedisClusterReconciler{
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
				Client: interceptor.NewClient(watchClient, interceptor.Funcs{
					Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						if _, ok := obj.(*appsv1.StatefulSet); ok {
							return errors.New("boom")
						}
						return c.Update(ctx, obj, opts...)
					},
				}),
			}
			_, err = failing.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).To(MatchError(ContainSubstring("boom")))

			degraded := conditionOf(get(), cachev1alpha1.ConditionDegraded)
			Expect(degraded.Status).To(Equal(metav1.ConditionTrue))
			Expect(degraded.Reason).To(Equal(reasonReconcileFailed))
			Expect(degraded.Message).To(ContainSubstring("boom"))

			reconcileOnce() // the next attempt succeeds
			Expect(conditionOf(get(), cachev1alpha1.ConditionDegraded).Status).To(Equal(metav1.ConditionFalse))
		})

		It("keeps rolloutComplete false until the StatefulSet has observed the latest spec", func() {
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			sts.Status = appsv1.StatefulSetStatus{
				ObservedGeneration: sts.Generation - 1, // stale: still describes the previous spec
				Replicas:           2, UpdatedReplicas: 2, ReadyReplicas: 2,
				CurrentRevision: "rev-1", UpdateRevision: "rev-1",
			}
			Expect(rolloutComplete(rc, sts)).To(BeFalse())
			sts.Status.ObservedGeneration = sts.Generation
			Expect(rolloutComplete(rc, sts)).To(BeTrue())
		})
	})

	Context("scaling", func() {
		BeforeEach(func() {
			newCluster(func(rc *cachev1alpha1.RedisCluster) { rc.Spec.Replicas = 2 })
			reconcileOnce()
			rolloutProgress(2, 2, 2, "rev-1", "rev-1")
			reconcileOnce()
			Expect(conditionOf(get(), cachev1alpha1.ConditionProgressing).Status).To(Equal(metav1.ConditionFalse))
			drainEvents()
		})

		statefulSetReplicas := func() int32 {
			GinkgoHelper()
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			return *sts.Spec.Replicas
		}

		It("scales up by updating the StatefulSet", func() {
			editSpec(func(s *cachev1alpha1.RedisClusterSpec) { s.Replicas = 5 })
			reconcileOnce()

			Expect(statefulSetReplicas()).To(Equal(int32(5)))
			Expect(drainEvents()).To(ConsistOf("Normal Scaled Scaled from 2 to 5 replicas"))
		})

		It("scales down by updating the StatefulSet", func() {
			editSpec(func(s *cachev1alpha1.RedisClusterSpec) { s.Replicas = 1 })
			reconcileOnce()

			Expect(statefulSetReplicas()).To(Equal(int32(1)))
			Expect(drainEvents()).To(ConsistOf("Normal Scaled Scaled from 2 to 1 replicas"))
		})

		It("reports Progressing until the new replicas are ready", func() {
			editSpec(func(s *cachev1alpha1.RedisClusterSpec) { s.Replicas = 4 })
			reconcileOnce()

			// The StatefulSet controller has not seen the new spec yet: its
			// status still shows 2/2 ready, which must not look like "done".
			progressing := conditionOf(get(), cachev1alpha1.ConditionProgressing)
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(reasonRolloutInProgress))

			rolloutProgress(3, 3, 2, "rev-1", "rev-1") // scale-up adds pods one by one
			reconcileOnce()
			Expect(conditionOf(get(), cachev1alpha1.ConditionProgressing).Status).To(Equal(metav1.ConditionTrue))

			rolloutProgress(4, 4, 4, "rev-1", "rev-1")
			reconcileOnce()
			got := get()
			Expect(conditionOf(got, cachev1alpha1.ConditionProgressing).Status).To(Equal(metav1.ConditionFalse))
			Expect(got.Status.ReadyReplicas).To(Equal(int32(4)))
			Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		})

		It("does not emit a Scaled event when replicas are unchanged", func() {
			editSpec(func(s *cachev1alpha1.RedisClusterSpec) {
				s.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")}
			})
			reconcileOnce()
			Expect(drainEvents()).To(BeEmpty())
		})
	})

	Context("rolling upgrade", func() {
		BeforeEach(func() {
			newCluster(func(rc *cachev1alpha1.RedisCluster) {
				rc.Spec.Replicas = 3
				rc.Spec.Version = "7.2"
			})
			reconcileOnce()
			rolloutProgress(3, 3, 3, "rev-1", "rev-1")
			reconcileOnce()
			Expect(get().Status.CurrentVersion).To(Equal("7.2"))
			drainEvents()
		})

		It("is configured to replace pods one at a time", func() {
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())

			Expect(sts.Spec.UpdateStrategy.Type).To(Equal(appsv1.RollingUpdateStatefulSetStrategyType))
			Expect(*sts.Spec.UpdateStrategy.RollingUpdate.Partition).To(BeZero())
			// OrderedReady waits for each pod to be Ready before touching the next.
			Expect(sts.Spec.PodManagementPolicy).To(Equal(appsv1.OrderedReadyPodManagement))
		})

		It("changes the image and announces the upgrade", func() {
			editSpec(func(s *cachev1alpha1.RedisClusterSpec) { s.Version = "7.4" })
			reconcileOnce()

			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("redis:7.4"))
			Expect(drainEvents()).To(ConsistOf("Normal Upgrading Rolling upgrade started from Redis 7.2 to 7.4"))
		})

		It("stays Progressing, keeping the old currentVersion, until every pod is updated", func() {
			editSpec(func(s *cachev1alpha1.RedisClusterSpec) { s.Version = "7.4" })
			reconcileOnce()

			// One of three pods has been replaced; the other two are still old.
			rolloutProgress(3, 1, 3, "rev-1", "rev-2")
			reconcileOnce()

			got := get()
			progressing := conditionOf(got, cachev1alpha1.ConditionProgressing)
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(reasonUpgradeInProgress))
			Expect(progressing.Message).To(ContainSubstring("from 7.2 to 7.4"))
			Expect(got.Status.CurrentVersion).To(Equal("7.2"))
			Expect(conditionOf(got, cachev1alpha1.ConditionAvailable).Status).To(Equal(metav1.ConditionTrue))
			Expect(drainEvents()).NotTo(ContainElement(ContainSubstring("Upgraded")))
		})

		It("completes: Progressing clears, currentVersion advances and Upgraded is emitted", func() {
			editSpec(func(s *cachev1alpha1.RedisClusterSpec) { s.Version = "7.4" })
			reconcileOnce()
			rolloutProgress(3, 1, 3, "rev-1", "rev-2")
			reconcileOnce()
			drainEvents()

			rolloutProgress(3, 3, 3, "rev-2", "rev-2")
			reconcileOnce()

			got := get()
			Expect(conditionOf(got, cachev1alpha1.ConditionProgressing).Status).To(Equal(metav1.ConditionFalse))
			Expect(got.Status.CurrentVersion).To(Equal("7.4"))
			Expect(drainEvents()).To(ConsistOf("Normal Upgraded Rolling upgrade finished: all replicas run Redis 7.4"))
		})
	})

	Context("deleting a RedisCluster", func() {
		It("releases the finalizer so deletion completes", func() {
			newCluster(nil)
			reconcileOnce()

			got := &cachev1alpha1.RedisCluster{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			Expect(k8sClient.Delete(ctx, got)).To(Succeed())

			// Still present: the finalizer blocks removal until we reconcile.
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			Expect(got.DeletionTimestamp.IsZero()).To(BeFalse())

			reconcileOnce()

			err := k8sClient.Get(ctx, key, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
