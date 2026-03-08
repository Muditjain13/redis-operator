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
	"bytes"
	"context"
	"errors"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	cachev1alpha1 "github.com/Muditjain13/redis-operator/api/v1alpha1"
)

// withInterceptor wraps the fixture's fake client so a test can make single
// calls fail.
func withInterceptor(t *testing.T, c client.Client, funcs interceptor.Funcs) client.Client {
	t.Helper()
	w, ok := c.(client.WithWatch)
	if !ok {
		t.Fatalf("%T cannot be wrapped", c)
	}
	return interceptor.NewClient(w, funcs)
}

func getSpec(t *testing.T, b *KubeBackend) (cachev1alpha1.RedisClusterSpec, string) {
	t.Helper()
	rc := &cachev1alpha1.RedisCluster{}
	if err := b.CR.Get(context.Background(), b.key(), rc); err != nil {
		t.Fatal(err)
	}
	return rc.Spec, rc.ResourceVersion
}

func TestResetSpecRestoresReplicasAndVersion(t *testing.T) {
	b := newKubeFixture(t)
	ctx := context.Background()
	if err := b.SetReplicas(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := b.SetVersion(ctx, "7.4"); err != nil {
		t.Fatal(err)
	}

	changed, err := b.ResetSpec(ctx, 3, "7.2")
	if err != nil || !changed {
		t.Fatalf("ResetSpec = %v, %v; want true, nil", changed, err)
	}
	if spec, _ := getSpec(t, b); spec.Replicas != 3 || spec.Version != "7.2" {
		t.Fatalf("spec after reset = %+v", spec)
	}
}

func TestResetSpecRestoresOneChangedField(t *testing.T) {
	b := newKubeFixture(t)
	ctx := context.Background()
	if err := b.SetVersion(ctx, "7.4"); err != nil {
		t.Fatal(err)
	}
	if changed, err := b.ResetSpec(ctx, 3, "7.2"); err != nil || !changed {
		t.Fatalf("ResetSpec = %v, %v; want true, nil", changed, err)
	}
	if spec, _ := getSpec(t, b); spec.Replicas != 3 || spec.Version != "7.2" {
		t.Fatalf("spec after reset = %+v", spec)
	}
}

func TestResetSpecWritesNothingWhenAlreadyReset(t *testing.T) {
	b := newKubeFixture(t)
	_, before := getSpec(t, b)

	changed, err := b.ResetSpec(context.Background(), 3, "7.2")
	if err != nil || changed {
		t.Fatalf("ResetSpec = %v, %v; want false, nil", changed, err)
	}
	// A write would bump the resource version and so the generation the
	// operator reconciles, for no reason.
	if _, after := getSpec(t, b); after != before {
		t.Fatalf("resourceVersion changed from %s to %s", before, after)
	}
}

func TestResetSpecOnMissingCluster(t *testing.T) {
	b := newKubeFixture(t)
	b.Cluster = "other"
	if _, err := b.ResetSpec(context.Background(), 3, "7.2"); !errors.Is(err, ErrClusterNotFound) {
		t.Fatalf("err = %v, want ErrClusterNotFound", err)
	}
}

func TestResetSpecReportsAPIErrors(t *testing.T) {
	boom := errors.New("boom")
	t.Run("get", func(t *testing.T) {
		b := newKubeFixture(t)
		b.CR = withInterceptor(t, b.CR, interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return boom
			},
		})
		if _, err := b.ResetSpec(context.Background(), 5, "7.4"); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	})
	t.Run("patch", func(t *testing.T) {
		b := newKubeFixture(t)
		b.CR = withInterceptor(t, b.CR, interceptor.Funcs{
			Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				return boom
			},
		})
		if _, err := b.ResetSpec(context.Background(), 5, "7.4"); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	})
}

type fakeResetter struct {
	changed bool
	err     error
	calls   int
	gotN    int32
	gotV    string
}

func (f *fakeResetter) ResetSpec(_ context.Context, n int32, v string) (bool, error) {
	f.calls++
	f.gotN, f.gotV = n, v
	return f.changed, f.err
}

func TestRunReset(t *testing.T) {
	tests := []struct {
		name    string
		r       *fakeResetter
		wantErr bool
	}{
		{"changed", &fakeResetter{changed: true}, false},
		{"already reset", &fakeResetter{}, false},
		{"failure", &fakeResetter{err: errors.New("boom")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runReset(context.Background(), tt.r, 3, "7.2")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.r.calls != 1 || tt.r.gotN != 3 || tt.r.gotV != "7.2" {
				t.Fatalf("resetter called %d times with %d, %q", tt.r.calls, tt.r.gotN, tt.r.gotV)
			}
		})
	}
}

func TestMainResetRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"reset", "--nope"}},
		{"stray argument", []string{"reset", "now"}},
		{"help", []string{"reset", "-h"}},
		{"zero replicas", []string{"reset", "--replicas=0"}},
		{"too many replicas", []string{"reset", "--replicas=8"}},
		{"bad timeout", []string{"reset", "--timeout=soon"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := Main(tt.args, nil, &out, &errOut); code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
		})
	}
}

func TestMainResetFailsWithoutKubernetesAccess(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("KUBECONFIG", empty+"/none")
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	var out, errOut bytes.Buffer
	if code := Main([]string{"reset"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d, want 1; output: %s%s", code, out.String(), errOut.String())
	}
}
