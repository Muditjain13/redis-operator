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

package ci_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const workflowPath = "../../.github/workflows/images.yml"

type workflow struct {
	Name        string            `yaml:"name"`
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress bool   `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]job `yaml:"jobs"`
}

type job struct {
	Name        string            `yaml:"name"`
	If          string            `yaml:"if"`
	Needs       string            `yaml:"needs"`
	RunsOn      string            `yaml:"runs-on"`
	Permissions map[string]string `yaml:"permissions"`
	Strategy    struct {
		Matrix struct {
			Include []map[string]string `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []step `yaml:"steps"`
}

type step struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

func loadWorkflow(t *testing.T) workflow {
	t.Helper()
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var wf workflow
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelt or unexpected key fails the test
	if err := dec.Decode(&wf); err != nil {
		t.Fatalf("%s: %v", workflowPath, err)
	}
	return wf
}

func (j job) stepUsing(prefix string) *step {
	for i := range j.Steps {
		if strings.HasPrefix(j.Steps[i].Uses, prefix) {
			return &j.Steps[i]
		}
	}
	return nil
}

func mustJob(t *testing.T, wf workflow, name string) job {
	t.Helper()
	j, ok := wf.Jobs[name]
	if !ok {
		t.Fatalf("job %q is missing; jobs are %v", name, reflect.ValueOf(wf.Jobs).MapKeys())
	}
	return j
}

func TestImagesWorkflowRunsOnlyForPushesToMain(t *testing.T) {
	wf := loadWorkflow(t)

	forbiddenTriggers := []string{"pull_request", "pull_request_target", "schedule", "workflow_run", "issue_comment"}
	for _, forbidden := range forbiddenTriggers {
		if _, ok := wf.On[forbidden]; ok {
			t.Errorf("trigger %q must not publish images or push to main", forbidden)
		}
	}
	push, ok := wf.On["push"].(map[string]any)
	if !ok {
		t.Fatalf("no push trigger: %v", wf.On)
	}
	if !reflect.DeepEqual(push["branches"], []any{"main"}) {
		t.Errorf("push branches = %v, want only main", push["branches"])
	}
	if _, ok := wf.On["workflow_dispatch"]; !ok {
		t.Error("a manual run should stay possible")
	}
	for name, j := range wf.Jobs {
		if j.If != "github.ref == 'refs/heads/main'" {
			t.Errorf("job %s: if = %q; a manual run from another branch must not publish", name, j.If)
		}
	}
}

func TestImagesWorkflowIgnoresOnlyDocsAndManifests(t *testing.T) {
	wf := loadWorkflow(t)
	push := wf.On["push"].(map[string]any)

	if _, ok := push["paths"]; ok {
		t.Error("`paths` would skip code changes that are not listed; use paths-ignore")
	}
	want := []any{"docs/**", "**/*.md", "deploy/**"}
	if !reflect.DeepEqual(push["paths-ignore"], want) {
		t.Errorf("paths-ignore = %v, want %v (code, Dockerfiles and the web UI must always rebuild)",
			push["paths-ignore"], want)
	}
}

func TestImagesWorkflowStartsWithNoPermissions(t *testing.T) {
	wf := loadWorkflow(t)
	if wf.Permissions == nil || len(wf.Permissions) != 0 {
		t.Errorf("workflow permissions = %v, want an explicit empty set `{}`", wf.Permissions)
	}
}

func TestOnlyTheBuildJobCanWritePackagesAndOnlyTheUpdateJobCanWriteContents(t *testing.T) {
	wf := loadWorkflow(t)

	build := mustJob(t, wf, "images")
	if want := map[string]string{"contents": "read", "packages": "write"}; !reflect.DeepEqual(build.Permissions, want) {
		t.Errorf("images job permissions = %v, want %v", build.Permissions, want)
	}
	update := mustJob(t, wf, "update-tags")
	if want := map[string]string{"contents": "write"}; !reflect.DeepEqual(update.Permissions, want) {
		t.Errorf("update-tags job permissions = %v, want %v", update.Permissions, want)
	}
	if len(wf.Jobs) != 2 {
		t.Errorf("%d jobs, want exactly images and update-tags", len(wf.Jobs))
	}
}

func TestOnlyTheUpdateJobKeepsTheCheckoutToken(t *testing.T) {
	wf := loadWorkflow(t)
	for name, j := range wf.Jobs {
		co := j.stepUsing("actions/checkout@")
		if co == nil {
			t.Errorf("job %s has no checkout step", name)
			continue
		}
		keeps := co.With["persist-credentials"] != false
		if want := name == "update-tags"; keeps != want {
			t.Errorf("job %s keeps the token = %v, want %v", name, keeps, want)
		}
	}
	if co := mustJob(t, wf, "update-tags").stepUsing("actions/checkout@"); co != nil && co.With["ref"] != "main" {
		t.Errorf("update-tags checks out %v, want main so its commit builds on the newest tags", co.With["ref"])
	}
}

func TestEveryActionIsPinnedToACommitWithAVersionComment(t *testing.T) {
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	pinned := regexp.MustCompile(`uses:\s+[\w./-]+@[0-9a-f]{40}\s+#\s+v\d+\.\d+\.\d+\s*$`)
	n := 0
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		n++
		if !pinned.MatchString(strings.TrimRight(line, "\r")) {
			t.Errorf("not pinned to a full commit SHA with a version comment: %s", strings.TrimSpace(line))
		}
	}
	if n < 5 {
		t.Errorf("found only %d actions; the check may not be reading the file", n)
	}
}

func TestImagesAreTaggedWithTheFullCommitSHA(t *testing.T) {
	wf := loadWorkflow(t)
	build := mustJob(t, wf, "images")

	push := build.stepUsing("docker/build-push-action@")
	if push == nil {
		t.Fatal("no build-and-push step")
	}
	if push.With["push"] != true {
		t.Errorf("push = %v, want true", push.With["push"])
	}
	if got, want := push.With["tags"], "${{ matrix.image }}:${{ github.sha }}"; got != want {
		t.Errorf("tags = %v, want %q: the tag is the full commit SHA and nothing else", got, want)
	}
	if got := push.With["platforms"]; got != "linux/amd64" {
		t.Errorf("platforms = %v; the VM is x86_64", got)
	}
	sourceLabel := "org.opencontainers.image.source=https://github.com/${{ github.repository }}"
	if labels, _ := push.With["labels"].(string); !strings.Contains(labels, sourceLabel) {
		t.Errorf("the source label that links the package to this repository is missing:\n%s", labels)
	}

	raw, _ := os.ReadFile(workflowPath)
	if strings.Contains(strings.ToLower(string(raw)), "latest") && strings.Contains(string(raw), ":latest") {
		t.Error("a :latest tag must never be published")
	}
}

func TestTheMatrixBuildsTheOperatorAndThePortal(t *testing.T) {
	build := mustJob(t, loadWorkflow(t), "images")
	want := []map[string]string{
		{"name": "operator", "image": "ghcr.io/muditjain13/redis-operator", "dockerfile": "Dockerfile"},
		{"name": "portal", "image": "ghcr.io/muditjain13/redis-portal", "dockerfile": "Dockerfile.portal"},
	}
	if !reflect.DeepEqual(build.Strategy.Matrix.Include, want) {
		t.Fatalf("matrix = %v, want %v", build.Strategy.Matrix.Include, want)
	}
	for _, m := range want {
		if m["image"] != strings.ToLower(m["image"]) {
			t.Errorf("registry names must be lower case: %s", m["image"])
		}
		if _, err := os.Stat(filepath.Join("..", "..", m["dockerfile"])); err != nil {
			t.Errorf("%s: %v", m["dockerfile"], err)
		}
	}
}

func TestTheUpdateJobWaitsForBothImagesAndPushesOnlyTheTwoOverlays(t *testing.T) {
	wf := loadWorkflow(t)
	update := mustJob(t, wf, "update-tags")

	if update.Needs != "images" {
		t.Errorf("needs = %q; tags must only be written after both images are pushed", update.Needs)
	}
	var script strings.Builder
	for _, s := range update.Steps {
		script.WriteString(s.Run + "\n")
		if strings.Contains(s.Run, "set-image-tag.sh") && s.Env["SHA"] != "${{ github.sha }}" {
			t.Errorf("step %q must pass the built commit as ${{ github.sha }}, got %q", s.Name, s.Env["SHA"])
		}
	}
	text := script.String()
	const (
		operator = "deploy/hosting/operator/kustomization.yaml redis-operator ghcr.io/muditjain13/redis-operator"
		portal   = "deploy/hosting/portal/kustomization.yaml redis-portal ghcr.io/muditjain13/redis-portal"
	)
	for _, want := range []string{
		"set-image-tag.sh " + operator + ` "$SHA"`,
		"set-image-tag.sh " + portal + ` "$SHA"`,
		"git add deploy/hosting/operator/kustomization.yaml deploy/hosting/portal/kustomization.yaml",
		"git push origin HEAD:main",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the update job does not run: %s", want)
		}
	}
	if strings.Contains(text, "git add .") || strings.Contains(text, "git add -A") || strings.Contains(text, "--force") {
		t.Error("the update job must stage only the two overlays and never force-push")
	}
	if _, err := os.Stat("../../hack/set-image-tag.sh"); err != nil {
		t.Error(err)
	}
}

func TestANewerPushReplacesAnOlderRun(t *testing.T) {
	wf := loadWorkflow(t)
	if wf.Concurrency.Group == "" || !wf.Concurrency.CancelInProgress {
		t.Errorf("concurrency = %+v; an older run must not overwrite newer tags", wf.Concurrency)
	}
}
