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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

const (
	redisPort          = 6379
	redisImageRepo     = "redis"
	redisContainerName = "redis"
	redisPortName      = "redis"

	configKey        = "redis.conf"
	configVolumeName = "config"
	configMountPath  = "/etc/redis"
	dataVolumeName   = "data"
	dataMountPath    = "/data"

	redisConfig = "appendonly yes\ndir /data\nprotected-mode no\n"
)

// The mutate functions below only set the fields the operator owns and leave
// fields defaulted by the API server untouched. That keeps CreateOrUpdate
// idempotent: when nothing has drifted, no Update call is issued.

func configMapName(rc *cachev1alpha1.RedisCluster) string {
	return rc.Name + "-config"
}

// selectorLabels identify the pods of one RedisCluster. They must never change
// because StatefulSet and Service selectors are immutable.
func selectorLabels(rc *cachev1alpha1.RedisCluster) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     "redis",
		"app.kubernetes.io/instance": rc.Name,
	}
}

func objectLabels(rc *cachev1alpha1.RedisCluster) map[string]string {
	labels := selectorLabels(rc)
	labels["app.kubernetes.io/managed-by"] = "redis-operator"
	return labels
}

func mutateConfigMap(cm *corev1.ConfigMap, rc *cachev1alpha1.RedisCluster) {
	cm.Labels = objectLabels(rc)
	cm.Data = map[string]string{configKey: redisConfig}
}

// mutateService makes the Service headless so every pod gets a stable DNS
// name (<pod>.<service>.<namespace>.svc) via the StatefulSet.
func mutateService(svc *corev1.Service, rc *cachev1alpha1.RedisCluster) {
	svc.Labels = objectLabels(rc)
	svc.Spec.ClusterIP = corev1.ClusterIPNone // immutable, always None
	svc.Spec.Selector = selectorLabels(rc)
	svc.Spec.Ports = []corev1.ServicePort{{
		Name:       redisPortName,
		Port:       redisPort,
		TargetPort: intstr.FromInt32(redisPort),
		Protocol:   corev1.ProtocolTCP,
	}}
}

func mutateStatefulSet(sts *appsv1.StatefulSet, rc *cachev1alpha1.RedisCluster) {
	sts.Labels = objectLabels(rc)

	// selector, serviceName and volumeClaimTemplates are immutable, so they are
	// only set when the StatefulSet is created.
	if sts.CreationTimestamp.IsZero() {
		sts.Spec.ServiceName = rc.Name
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorLabels(rc)}
		sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{
			ObjectMeta: metav1.ObjectMeta{Name: dataVolumeName},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: rc.Spec.Storage.Size},
				},
			},
		}}
	}

	sts.Spec.Replicas = new(rc.Spec.Replicas)

	// RollingUpdate replaces pods one at a time, highest ordinal first, and
	// waits for each to become ready before moving on.
	sts.Spec.UpdateStrategy.Type = appsv1.RollingUpdateStatefulSetStrategyType
	if sts.Spec.UpdateStrategy.RollingUpdate == nil {
		sts.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{}
	}
	sts.Spec.UpdateStrategy.RollingUpdate.Partition = ptr.To[int32](0)

	sts.Spec.Template.Labels = objectLabels(rc)
	mutatePodSpec(&sts.Spec.Template.Spec, rc)
}

func mutatePodSpec(pod *corev1.PodSpec, rc *cachev1alpha1.RedisCluster) {
	pod.Volumes = []corev1.Volume{{
		Name: configVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(rc)},
				DefaultMode:          ptr.To[int32](0o644),
			},
		},
	}}

	idx := -1
	for i := range pod.Containers {
		if pod.Containers[i].Name == redisContainerName {
			idx = i
			break
		}
	}
	if idx < 0 {
		pod.Containers = append(pod.Containers, corev1.Container{Name: redisContainerName})
		idx = len(pod.Containers) - 1
	}
	mutateRedisContainer(&pod.Containers[idx], rc)
}

func mutateRedisContainer(c *corev1.Container, rc *cachev1alpha1.RedisCluster) {
	c.Image = fmt.Sprintf("%s:%s", redisImageRepo, rc.Spec.Version)
	c.ImagePullPolicy = corev1.PullIfNotPresent
	// Args (not Command) keep the image entrypoint, which fixes /data
	// ownership and drops root privileges before starting redis-server.
	c.Args = []string{configMountPath + "/" + configKey}
	c.Ports = []corev1.ContainerPort{{
		Name:          redisPortName,
		ContainerPort: redisPort,
		Protocol:      corev1.ProtocolTCP,
	}}
	c.Resources = rc.Spec.Resources
	c.LivenessProbe = redisProbe(15)
	c.ReadinessProbe = redisProbe(5)
	c.VolumeMounts = []corev1.VolumeMount{
		{Name: dataVolumeName, MountPath: dataMountPath},
		{Name: configVolumeName, MountPath: configMountPath, ReadOnly: true},
	}
}

// redisProbe pings Redis and requires a PONG reply. redis-cli can exit 0 on
// an error reply (e.g. while loading the dataset), so the output is checked.
func redisProbe(initialDelaySeconds int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"sh", "-c", "redis-cli ping | grep -q PONG"},
			},
		},
		InitialDelaySeconds: initialDelaySeconds,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		SuccessThreshold:    1,
		FailureThreshold:    3,
	}
}
