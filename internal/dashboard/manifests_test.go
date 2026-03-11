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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

// These tests read the manifests under deploy/portal and pin the properties
// the portal's security model depends on, so a careless edit fails the build.

const portalDir = "../../deploy/portal"

type manifestKind struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

// manifestDocs returns every YAML document in the file, raw.
func manifestDocs(t *testing.T, name string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(portalDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var docs [][]byte
	for d := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n---\n") {
		if strings.TrimSpace(d) != "" {
			docs = append(docs, []byte(d))
		}
	}
	return docs
}

// loadAs finds the one document of the given kind and name and decodes it
// strictly, so a misspelt field fails the test.
func loadAs[T any](t *testing.T, file, kind, name string) T {
	t.Helper()
	for _, doc := range manifestDocs(t, file) {
		var k manifestKind
		if err := yaml.Unmarshal(doc, &k); err != nil {
			t.Fatal(err)
		}
		if k.Kind == kind && k.Metadata.Name == name {
			var out T
			if err := yaml.UnmarshalStrict(doc, &out); err != nil {
				t.Fatalf("%s %s: %v", kind, name, err)
			}
			return out
		}
	}
	t.Fatalf("%s %q not found in %s", kind, name, file)
	panic("unreachable")
}

func TestPortalNeverUsesClusterWideRBAC(t *testing.T) {
	entries, err := os.ReadDir(portalDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		for _, doc := range manifestDocs(t, e.Name()) {
			var k manifestKind
			if err := yaml.Unmarshal(doc, &k); err != nil {
				t.Fatal(err)
			}
			if k.Kind == "ClusterRole" || k.Kind == "ClusterRoleBinding" {
				t.Errorf("%s contains a %s; the portal may only use namespaced RBAC", e.Name(), k.Kind)
			}
			if k.Kind == "kustomization" || k.Kind == "Kustomization" {
				continue
			}
			if k.Metadata.Namespace != "demo" {
				t.Errorf("%s %s is in namespace %q, want demo", k.Kind, k.Metadata.Name, k.Metadata.Namespace)
			}
		}
	}
}

func TestPortalRoleGrantsOnlyWhatTheButtonsNeed(t *testing.T) {
	role := loadAs[rbacv1.Role](t, "rbac.yaml", "Role", "redis-portal")
	want := []rbacv1.PolicyRule{
		{APIGroups: []string{"cache.mudit.dev"}, Resources: []string{"redisclusters"}, Verbs: []string{"get", "patch"}},
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"list", "watch", "delete"}},
		{APIGroups: []string{"apps"}, Resources: []string{"statefulsets"}, Verbs: []string{"get", "delete"}},
		{APIGroups: []string{"events.k8s.io"}, Resources: []string{"events"}, Verbs: []string{"list", "watch"}},
	}
	if !reflect.DeepEqual(role.Rules, want) {
		t.Fatalf("portal Role rules changed:\n got %+v\nwant %+v", role.Rules, want)
	}
}

func TestResetRoleCanOnlyReadAndPatchTheRedisCluster(t *testing.T) {
	role := loadAs[rbacv1.Role](t, "rbac.yaml", "Role", "redis-portal-reset")
	want := []rbacv1.PolicyRule{
		{APIGroups: []string{"cache.mudit.dev"}, Resources: []string{"redisclusters"}, Verbs: []string{"get", "patch"}},
	}
	if !reflect.DeepEqual(role.Rules, want) {
		t.Fatalf("reset Role rules changed:\n got %+v\nwant %+v", role.Rules, want)
	}
}

func TestRoleBindingsPointAtTheMatchingRoleAndServiceAccount(t *testing.T) {
	for _, name := range []string{"redis-portal", "redis-portal-reset"} {
		rb := loadAs[rbacv1.RoleBinding](t, "rbac.yaml", "RoleBinding", name)
		if rb.RoleRef.Kind != "Role" || rb.RoleRef.Name != name {
			t.Errorf("%s binds %+v, want Role %s", name, rb.RoleRef, name)
		}
		if len(rb.Subjects) != 1 || rb.Subjects[0].Kind != "ServiceAccount" ||
			rb.Subjects[0].Name != name || rb.Subjects[0].Namespace != "demo" {
			t.Errorf("%s subjects = %+v", name, rb.Subjects)
		}
		// Both ServiceAccounts must exist.
		loadAs[corev1.ServiceAccount](t, "rbac.yaml", "ServiceAccount", name)
	}
}

// hardened checks the container settings both workloads must share.
func hardened(t *testing.T, who string, pod corev1.PodSpec) {
	t.Helper()
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Errorf("%s does not require a non-root user", who)
	}
	if len(pod.Containers) != 1 {
		t.Fatalf("%s has %d containers, want 1", who, len(pod.Containers))
	}
	sc := pod.Containers[0].SecurityContext
	switch {
	case sc == nil:
		t.Fatalf("%s container has no securityContext", who)
	case sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation:
		t.Errorf("%s allows privilege escalation", who)
	case sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem:
		t.Errorf("%s root filesystem is writable", who)
	case sc.Capabilities == nil || !reflect.DeepEqual(sc.Capabilities.Drop, []corev1.Capability{"ALL"}):
		t.Errorf("%s does not drop all capabilities", who)
	}
	if pod.Containers[0].Resources.Limits == nil {
		t.Errorf("%s has no resource limits", who)
	}
}

func TestPortalDeployment(t *testing.T) {
	d := loadAs[appsv1.Deployment](t, "deployment.yaml", "Deployment", "redis-portal")
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 1 {
		t.Errorf("replicas = %v; the portal keeps its state in memory and must run as one", d.Spec.Replicas)
	}
	if d.Spec.Template.Spec.ServiceAccountName != "redis-portal" {
		t.Errorf("service account = %q", d.Spec.Template.Spec.ServiceAccountName)
	}
	hardened(t, "portal Deployment", d.Spec.Template.Spec)

	// The pod labels must match the selector, and must not match the Redis
	// Pods the portal lists (app.kubernetes.io/name=redis).
	if got := d.Spec.Template.Labels["app.kubernetes.io/name"]; got != "redis-portal" {
		t.Errorf("pod label name = %q", got)
	}
	for _, a := range d.Spec.Template.Spec.Containers[0].Args {
		if strings.Contains(a, "trust-proxy-headers") {
			t.Errorf("the base manifest must not trust proxy headers: %s", a)
		}
	}
}

func TestResetCronJob(t *testing.T) {
	c := loadAs[batchv1.CronJob](t, "reset-cronjob.yaml", "CronJob", "redis-portal-reset")
	if c.Spec.Schedule != "*/10 * * * *" {
		t.Errorf("schedule = %q, want every 10 minutes", c.Spec.Schedule)
	}
	if c.Spec.ConcurrencyPolicy != batchv1.ForbidConcurrent {
		t.Errorf("concurrencyPolicy = %q, want Forbid", c.Spec.ConcurrencyPolicy)
	}
	pod := c.Spec.JobTemplate.Spec.Template.Spec
	if pod.ServiceAccountName != "redis-portal-reset" {
		t.Errorf("service account = %q; the job must not run as the portal", pod.ServiceAccountName)
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q", pod.RestartPolicy)
	}
	hardened(t, "reset CronJob", pod)

	args := pod.Containers[0].Args
	if len(args) == 0 || args[0] != "reset" {
		t.Fatalf("args = %v, want the reset subcommand first", args)
	}
	for _, want := range []string{"--replicas=3", "--version=7.2", "--namespace=demo"} {
		found := false
		for _, a := range args {
			found = found || a == want
		}
		if !found {
			t.Errorf("args %v lack %s", args, want)
		}
	}
}

// The arguments in the manifests must be flags the binary actually accepts.
func TestManifestArgumentsAreAcceptedByTheBinary(t *testing.T) {
	d := loadAs[appsv1.Deployment](t, "deployment.yaml", "Deployment", "redis-portal")
	c := loadAs[batchv1.CronJob](t, "reset-cronjob.yaml", "CronJob", "redis-portal-reset")

	check := func(who string, args []string) {
		var out, errOut strings.Builder
		// An empty kubeconfig makes the run fail with exit code 1 after the
		// flags were parsed; a flag error would be exit code 2.
		empty := t.TempDir()
		t.Setenv("KUBECONFIG", empty+"/none")
		t.Setenv("HOME", empty)
		t.Setenv("USERPROFILE", empty)
		t.Setenv("KUBERNETES_SERVICE_HOST", "")
		t.Setenv("KUBERNETES_SERVICE_PORT", "")
		if code := Main(args, nil, &out, &errOut); code == 2 {
			t.Errorf("%s: the binary rejects the manifest arguments %v: %s", who, args, errOut.String())
		}
	}
	check("portal Deployment", d.Spec.Template.Spec.Containers[0].Args)
	check("reset CronJob", c.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Args)
}
