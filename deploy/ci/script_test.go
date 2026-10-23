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

// Package ci_test checks the CI workflow that publishes the images and the
// script it uses to point the hosted deployment at them, so a careless edit
// cannot publish from a pull request, widen a token, or write a bad tag.
package ci_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	scriptPath = "../../hack/set-image-tag.sh"
	portalRepo = "ghcr.io/muditjain13/redis-portal"
	sha1       = "0123456789abcdef0123456789abcdef01234567"
	sha2       = "fedcba9876543210fedcba9876543210fedcba98"
)

// runScript runs hack/set-image-tag.sh with bash and returns its combined output.
func runScript(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// bash on a plain Windows PATH may be WSL's, which cannot read these paths.
		t.Skip("run in WSL or CI")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	abs, err := filepath.Abs(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bash, append([]string{abs}, args...)...).CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kustomization.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const legacy = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ../../portal

images:
  - name: redis-portal
    newTag: hosted

patches:
  - target:
      kind: Deployment
      name: redis-portal
    patch: |-
      - op: add
        path: /spec/template/spec/containers/0/args/-
        value: --trust-proxy-headers
`

func TestSetImageTagConvertsALegacyEntry(t *testing.T) {
	path := writeFile(t, legacy)
	if out, err := runScript(t, path, "redis-portal", portalRepo, sha1); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	want := strings.Replace(legacy,
		"    newTag: hosted\n",
		"    newName: "+portalRepo+"\n    newTag: \""+sha1+"\"\n", 1)
	if got := readFile(t, path); got != want {
		t.Fatalf("unexpected result:\n%s\nwant:\n%s", got, want)
	}
}

func TestSetImageTagReplacesAnExistingTagAndIsRepeatable(t *testing.T) {
	path := writeFile(t, legacy)
	for _, sha := range []string{sha1, sha1, sha2} {
		if out, err := runScript(t, path, "redis-portal", portalRepo, sha); err != nil {
			t.Fatalf("script failed: %v\n%s", err, out)
		}
	}
	got := readFile(t, path)
	if strings.Contains(got, sha1) {
		t.Errorf("the old tag is still there:\n%s", got)
	}
	if n := strings.Count(got, "newTag:"); n != 1 {
		t.Errorf("%d newTag lines, want exactly 1:\n%s", n, got)
	}
	if n := strings.Count(got, "newName:"); n != 1 {
		t.Errorf("%d newName lines, want exactly 1:\n%s", n, got)
	}
	if !strings.Contains(got, `newTag: "`+sha2+`"`) {
		t.Errorf("the new tag is missing or unquoted:\n%s", got)
	}
}

func TestSetImageTagTouchesOnlyTheNamedImage(t *testing.T) {
	const two = `images:
  - name: redis-operator
    newName: ghcr.io/muditjain13/redis-operator
    newTag: "` + sha2 + `"
  - name: redis-portal
    newName: ` + portalRepo + `
    newTag: "` + sha2 + `"
    digest: sha256:abc
  - name: something-else
    newTag: keep-me
`
	path := writeFile(t, two)
	if out, err := runScript(t, path, "redis-portal", portalRepo, sha1); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "ghcr.io/muditjain13/redis-operator\n    newTag: \""+sha2+"\"") {
		t.Errorf("the operator entry changed:\n%s", got)
	}
	if !strings.Contains(got, "  - name: something-else\n    newTag: keep-me\n") {
		t.Errorf("the unrelated entry changed:\n%s", got)
	}
	if strings.Contains(got, "digest:") {
		t.Errorf("a stale digest would override the new tag and must be dropped:\n%s", got)
	}
	if !strings.Contains(got, "newTag: \""+sha1+"\"") {
		t.Errorf("the portal entry was not updated:\n%s", got)
	}
}

func TestSetImageTagToleratesWindowsLineEndings(t *testing.T) {
	path := writeFile(t, strings.ReplaceAll(legacy, "\n", "\r\n"))
	if out, err := runScript(t, path, "redis-portal", portalRepo, sha1); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if got := readFile(t, path); !strings.Contains(got, "newTag: \""+sha1+"\"\n") {
		t.Errorf("not converted:\n%q", got)
	}
}

func TestSetImageTagRefusesBadInputAndLeavesTheFileAlone(t *testing.T) {
	tests := []struct {
		name  string
		image string
		repo  string
		sha   string
	}{
		{"short sha", "redis-portal", portalRepo, "abc1234"},
		{"39 characters", "redis-portal", portalRepo, sha1[:39]},
		{"upper case sha", "redis-portal", portalRepo, strings.ToUpper(sha1)},
		{"a branch name", "redis-portal", portalRepo, "main"},
		{"latest", "redis-portal", portalRepo, "latest"},
		{"someone else's registry", "redis-portal", "ghcr.io/other/redis-portal", sha1},
		{"a different registry", "redis-portal", "docker.io/muditjain13/redis-portal", sha1},
		{"upper case repository", "redis-portal", "ghcr.io/Muditjain13/redis-portal", sha1},
		{"an image that is not listed", "no-such-image", portalRepo, sha1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, legacy)
			if out, err := runScript(t, path, tt.image, tt.repo, tt.sha); err == nil {
				t.Fatalf("expected an error, got success:\n%s", out)
			}
			if got := readFile(t, path); got != legacy {
				t.Errorf("the file changed after a refused run:\n%s", got)
			}
		})
	}
}

func TestSetImageTagNeedsAllFourArguments(t *testing.T) {
	if out, err := runScript(t, "only", "three", "args"); err == nil {
		t.Fatalf("expected a usage error, got:\n%s", out)
	}
}
