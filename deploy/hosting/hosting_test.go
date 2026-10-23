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

// Package hosting_test checks the manifests for the hosted k3s deployment. The
// portal there trusts the CF-Connecting-IP header, which is only safe while
// nothing but cloudflared can reach it, so these tests pin that.
package hosting_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/yaml"
)

func load[T any](t *testing.T, file string) T {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := yaml.UnmarshalStrict(data, &out); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return out
}

func TestPortalOverlayOnlyAdmitsCloudflared(t *testing.T) {
	np := load[networkingv1.NetworkPolicy](t, "portal/networkpolicy.yaml")

	if np.Namespace != "demo" || np.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"] != "redis-portal" {
		t.Errorf("policy selects %s/%v, want the redis-portal pods in demo", np.Namespace, np.Spec.PodSelector.MatchLabels)
	}
	if !reflect.DeepEqual(np.Spec.PolicyTypes, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}) {
		t.Errorf("policyTypes = %v, want only Ingress (the portal must still reach the API server)", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].From) != 1 || len(np.Spec.Ingress[0].Ports) != 1 {
		t.Fatalf("ingress = %+v, want exactly one source and one port", np.Spec.Ingress)
	}
	rule := np.Spec.Ingress[0]
	from := rule.From[0]
	if from.NamespaceSelector == nil || from.PodSelector == nil || from.IPBlock != nil {
		t.Fatalf("source = %+v, want a namespace selector and a pod selector together", from)
	}
	if got := from.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; got != "cloudflared" {
		t.Errorf("source namespace = %q, want cloudflared", got)
	}
	if got := from.PodSelector.MatchLabels["app.kubernetes.io/name"]; got != "cloudflared" {
		t.Errorf("source pod = %q, want cloudflared", got)
	}
	if rule.Ports[0].Port == nil || rule.Ports[0].Port.IntValue() != 8080 {
		t.Errorf("port = %v, want 8080", rule.Ports[0].Port)
	}
}

func TestPortalOverlayTurnsOnProxyHeadersAndUsesTheHostedImage(t *testing.T) {
	data, err := os.ReadFile("portal/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	k := string(data)
	for _, want := range []string{"--trust-proxy-headers", "networkpolicy.yaml", "../../portal", "newTag: hosted"} {
		if !strings.Contains(k, want) {
			t.Errorf("portal/kustomization.yaml lacks %q", want)
		}
	}
	// The flag must never be on in a setup without the network policy.
	base, err := os.ReadFile("../portal/deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(base), "trust-proxy-headers") {
		t.Error("deploy/portal/deployment.yaml trusts proxy headers; only the hosted overlay may")
	}
}

func TestOperatorOverlayUsesTheHostedImage(t *testing.T) {
	data, err := os.ReadFile("operator/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "newTag: hosted") || !strings.Contains(string(data), "../../operator") {
		t.Errorf("operator overlay = %s", data)
	}
}

// tunnelConfig is the part of cloudflared's config.yaml the tests look at.
type tunnelConfig struct {
	Tunnel          string `json:"tunnel"`
	CredentialsFile string `json:"credentials-file"`
	NoAutoupdate    bool   `json:"no-autoupdate"`
	Metrics         string `json:"metrics"`
	Ingress         []struct {
		Hostname string `json:"hostname"`
		Service  string `json:"service"`
	} `json:"ingress"`
}

func TestCloudflaredRoutesOnlyTheDemoHostnameToThePortal(t *testing.T) {
	cm := load[corev1.ConfigMap](t, "cloudflared/configmap.yaml")
	var cfg tunnelConfig
	if err := yaml.UnmarshalStrict([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		t.Fatal(err)
	}

	if len(cfg.Tunnel) != 36 || strings.Count(cfg.Tunnel, "-") != 4 {
		t.Errorf("tunnel = %q, want the tunnel's UUID", cfg.Tunnel)
	}
	// Exactly one named route, then a catch-all that refuses everything else.
	// Without the catch-all, anything reaching the tunnel could be proxied.
	if len(cfg.Ingress) != 2 {
		t.Fatalf("ingress = %+v, want one hostname rule and a 404 catch-all", cfg.Ingress)
	}
	if got := cfg.Ingress[0]; got.Hostname != "redisoperator.dpdns.org" ||
		got.Service != "http://redis-portal.demo.svc.cluster.local:80" {
		t.Errorf("first rule = %+v, want redisoperator.dpdns.org to the portal Service", got)
	}
	if got := cfg.Ingress[1]; got.Hostname != "" || got.Service != "http_status:404" {
		t.Errorf("last rule = %+v, want a catch-all returning 404", got)
	}
	if !cfg.NoAutoupdate {
		t.Error("a container must not update itself in place")
	}

	// The ConfigMap is in git, so it must hold no credential material.
	for _, banned := range []string{"AccountTag", "TunnelSecret", "TunnelID", "BEGIN ARGO TUNNEL TOKEN"} {
		if strings.Contains(cm.Data["config.yaml"], banned) {
			t.Errorf("config.yaml contains %q, which belongs in the Secret only", banned)
		}
	}
}

// loadCloudflared returns the cloudflared Deployment, its pod spec and its one container.
func loadCloudflared(t *testing.T) (appsv1.Deployment, corev1.PodSpec, corev1.Container) {
	t.Helper()
	d := load[appsv1.Deployment](t, "cloudflared/deployment.yaml")
	pod := d.Spec.Template.Spec
	if len(pod.Containers) != 1 {
		t.Fatalf("%d containers, want 1", len(pod.Containers))
	}
	return d, pod, pod.Containers[0]
}

func TestCloudflaredKeepsItsCredentialsOutOfGit(t *testing.T) {
	_, pod, c := loadCloudflared(t)

	// No token or credential value may be written into the manifest.
	for _, e := range c.Env {
		if e.Value != "" && strings.Contains(strings.ToUpper(e.Name), "TOKEN") {
			t.Errorf("env %s has a literal value", e.Name)
		}
	}

	// The credentials file comes from a Secret, at the path config.yaml names.
	var secretVol, configVol *corev1.Volume
	for i := range pod.Volumes {
		switch pod.Volumes[i].Name {
		case "credentials":
			secretVol = &pod.Volumes[i]
		case "config":
			configVol = &pod.Volumes[i]
		}
	}
	switch {
	case secretVol == nil || secretVol.Secret == nil:
		t.Fatal("no credentials volume backed by a Secret")
	case secretVol.Secret.SecretName != "cloudflared-credentials":
		t.Errorf("credentials Secret = %q, want cloudflared-credentials", secretVol.Secret.SecretName)
	case secretVol.Secret.DefaultMode == nil || *secretVol.Secret.DefaultMode != 0o440:
		t.Errorf("credentials file mode = %v, want 0440 (readable by the pod's group only)", secretVol.Secret.DefaultMode)
	}
	if configVol == nil || configVol.ConfigMap == nil || configVol.ConfigMap.Name != "cloudflared-config" {
		t.Errorf("config volume = %+v, want ConfigMap cloudflared-config", configVol)
	}
	if pod.SecurityContext == nil || pod.SecurityContext.FSGroup == nil || *pod.SecurityContext.FSGroup != 65532 {
		t.Error("fsGroup must match the user, or the 0440 credentials file is unreadable")
	}
}

func TestCloudflaredConfigMatchesTheMounts(t *testing.T) {
	_, _, c := loadCloudflared(t)
	cm := load[corev1.ConfigMap](t, "cloudflared/configmap.yaml")
	var cfg tunnelConfig
	if err := yaml.UnmarshalStrict([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		t.Fatal(err)
	}

	mounts := map[string]string{}
	for _, m := range c.VolumeMounts {
		if !m.ReadOnly {
			t.Errorf("mount %s is writable", m.Name)
		}
		mounts[m.Name] = m.MountPath
	}
	if want := "/etc/cloudflared/credentials/credentials.json"; cfg.CredentialsFile != want ||
		!strings.HasPrefix(cfg.CredentialsFile, mounts["credentials"]+"/") {
		t.Errorf("config says credentials-file %q, mount is %q", cfg.CredentialsFile, mounts["credentials"])
	}
	if !reflect.DeepEqual(c.Args, []string{"tunnel", "--config", mounts["config"] + "/config.yaml", "run"}) {
		t.Errorf("args = %v, want tunnel --config <mount>/config.yaml run", c.Args)
	}
}

func TestCloudflaredIsHardened(t *testing.T) {
	d, pod, c := loadCloudflared(t)

	if _, tag, ok := strings.Cut(c.Image, ":"); !ok || tag == "" || tag == "latest" {
		t.Errorf("image %q must have a pinned tag", c.Image)
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("cloudflared does not need the Kubernetes API, so it should not mount a token")
	}
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Error("cloudflared should run as non-root")
	}
	sc := c.SecurityContext
	if sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem ||
		sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation ||
		sc.Capabilities == nil || !reflect.DeepEqual(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		t.Errorf("container securityContext = %+v", sc)
	}
	if d.Namespace != "cloudflared" {
		t.Errorf("namespace = %q; the network policy only admits the cloudflared namespace", d.Namespace)
	}
	if d.Spec.Template.Labels["app.kubernetes.io/name"] != "cloudflared" {
		t.Error("pod label app.kubernetes.io/name=cloudflared is what the network policy admits")
	}
}
