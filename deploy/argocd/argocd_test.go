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

// Package argocd_test checks the Argo CD Application manifests in this
// directory, so a careless edit cannot switch off self-heal or start
// reverting the fields the portal changes on purpose.
package argocd_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

const repoURL = "https://github.com/Muditjain13/redis-operator.git"

// app is the part of an Application these tests look at.
type app struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name       string   `json:"name"`
		Namespace  string   `json:"namespace"`
		Finalizers []string `json:"finalizers"`
	} `json:"metadata"`
	Spec struct {
		Project string `json:"project"`
		Source  struct {
			RepoURL        string `json:"repoURL"`
			TargetRevision string `json:"targetRevision"`
			Path           string `json:"path"`
		} `json:"source"`
		Destination struct {
			Server    string `json:"server"`
			Namespace string `json:"namespace"`
		} `json:"destination"`
		IgnoreDifferences []struct {
			Group        string   `json:"group"`
			Kind         string   `json:"kind"`
			Name         string   `json:"name"`
			JSONPointers []string `json:"jsonPointers"`
		} `json:"ignoreDifferences"`
		SyncPolicy struct {
			Automated struct {
				Prune    bool `json:"prune"`
				SelfHeal bool `json:"selfHeal"`
			} `json:"automated"`
			SyncOptions []string `json:"syncOptions"`
			Retry       struct {
				Limit   int `json:"limit"`
				Backoff struct {
					Duration    string `json:"duration"`
					Factor      int    `json:"factor"`
					MaxDuration string `json:"maxDuration"`
				} `json:"backoff"`
			} `json:"retry"`
		} `json:"syncPolicy"`
	} `json:"spec"`
}

func load(t *testing.T, file string) app {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var a app
	if err := yaml.UnmarshalStrict(data, &a); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return a
}

func TestEveryApplicationFollowsTheSamePolicy(t *testing.T) {
	for _, f := range []string{"operator.yaml", "portal.yaml"} {
		a := load(t, f)
		if a.APIVersion != "argoproj.io/v1alpha1" || a.Kind != "Application" {
			t.Errorf("%s is a %s %s", f, a.APIVersion, a.Kind)
		}
		if a.Metadata.Namespace != "argocd" {
			t.Errorf("%s: namespace = %q, want argocd", f, a.Metadata.Namespace)
		}
		if !slices.Contains(a.Metadata.Finalizers, "resources-finalizer.argocd.argoproj.io") {
			t.Errorf("%s: deleting the Application would leave its resources behind", f)
		}
		src := a.Spec.Source
		if src.RepoURL != repoURL || src.TargetRevision != "main" {
			t.Errorf("%s: source = %+v, want %s at main", f, src, repoURL)
		}
		if a.Spec.Destination.Server != "https://kubernetes.default.svc" {
			t.Errorf("%s: destination server = %q", f, a.Spec.Destination.Server)
		}
		sp := a.Spec.SyncPolicy
		if !sp.Automated.Prune || !sp.Automated.SelfHeal {
			t.Errorf("%s: automated sync needs prune and selfHeal both on, got %+v", f, sp.Automated)
		}
		if sp.Retry.Limit == 0 {
			t.Errorf("%s: no retry, so a first sync that races the other Application would stay failed", f)
		}
		// The path must be a kustomize directory in this repository.
		kustomization := filepath.Join("..", "..", filepath.FromSlash(src.Path), "kustomization.yaml")
		if _, err := os.Stat(kustomization); err != nil {
			t.Errorf("%s: path %q has no kustomization.yaml: %v", f, src.Path, err)
		}
	}
}

func TestOperatorApplication(t *testing.T) {
	a := load(t, "operator.yaml")
	if a.Spec.Source.Path != "deploy/operator" || a.Spec.Destination.Namespace != "redis-operator-system" {
		t.Errorf("source path %q, destination namespace %q", a.Spec.Source.Path, a.Spec.Destination.Namespace)
	}
	if !slices.Contains(a.Spec.SyncPolicy.SyncOptions, "ServerSideApply=true") {
		t.Error("the CRD should be applied server side")
	}
	if len(a.Spec.IgnoreDifferences) != 0 {
		t.Errorf("the operator Application should ignore nothing, got %+v", a.Spec.IgnoreDifferences)
	}
}

func TestPortalApplicationLeavesTheButtonFieldsAlone(t *testing.T) {
	a := load(t, "portal.yaml")
	if a.Spec.Source.Path != "deploy/portal" || a.Spec.Destination.Namespace != "demo" {
		t.Errorf("source path %q, destination namespace %q", a.Spec.Source.Path, a.Spec.Destination.Namespace)
	}

	// Exactly these two fields of exactly this one resource: ignoring more
	// would hide real drift from Argo CD.
	if len(a.Spec.IgnoreDifferences) != 1 {
		t.Fatalf("ignoreDifferences = %+v, want one entry", a.Spec.IgnoreDifferences)
	}
	ig := a.Spec.IgnoreDifferences[0]
	if ig.Group != "cache.mudit.dev" || ig.Kind != "RedisCluster" || ig.Name != "rediscluster-sample" {
		t.Errorf("ignored resource = %s/%s %s", ig.Group, ig.Kind, ig.Name)
	}
	if !reflect.DeepEqual(ig.JSONPointers, []string{"/spec/replicas", "/spec/version"}) {
		t.Errorf("ignored fields = %v, want /spec/replicas and /spec/version", ig.JSONPointers)
	}
	for _, opt := range []string{"RespectIgnoreDifferences=true", "SkipDryRunOnMissingResource=true"} {
		if !slices.Contains(a.Spec.SyncPolicy.SyncOptions, opt) {
			t.Errorf("syncOptions lack %s", opt)
		}
	}
}

// Every Application names project "default", so that project has to exist even
// on a core install, which has no API server to create it.
func TestDefaultProjectIsDefined(t *testing.T) {
	data, err := os.ReadFile("appproject.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Kind != "AppProject" || p.Metadata.Name != "default" || p.Metadata.Namespace != "argocd" {
		t.Fatalf("appproject.yaml defines %s %s/%s", p.Kind, p.Metadata.Namespace, p.Metadata.Name)
	}
	for _, f := range []string{"operator.yaml", "portal.yaml"} {
		if got := load(t, f).Spec.Project; got != p.Metadata.Name {
			t.Errorf("%s uses project %q, want %q", f, got, p.Metadata.Name)
		}
	}
}
