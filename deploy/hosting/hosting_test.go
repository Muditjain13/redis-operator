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

func TestCloudflaredKeepsItsTokenOutOfGit(t *testing.T) {
	d := load[appsv1.Deployment](t, "cloudflared/deployment.yaml")
	pod := d.Spec.Template.Spec
	if len(pod.Containers) != 1 {
		t.Fatalf("%d containers, want 1", len(pod.Containers))
	}
	c := pod.Containers[0]

	var token *corev1.EnvVar
	for i := range c.Env {
		if c.Env[i].Name == "TUNNEL_TOKEN" {
			token = &c.Env[i]
		}
	}
	switch {
	case token == nil:
		t.Fatal("TUNNEL_TOKEN is not set")
	case token.Value != "":
		t.Error("TUNNEL_TOKEN has a literal value; it must come from a Secret")
	case token.ValueFrom == nil || token.ValueFrom.SecretKeyRef == nil ||
		token.ValueFrom.SecretKeyRef.Name != "cloudflared-token" || token.ValueFrom.SecretKeyRef.Key != "token":
		t.Errorf("TUNNEL_TOKEN comes from %+v, want Secret cloudflared-token key token", token.ValueFrom)
	}

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
