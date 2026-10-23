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
	"os"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// These checks tie the hosted overlays to the workflow that rewrites them, so
// one cannot drift away from the other.

type kustomization struct {
	Images []struct {
		Name    string `yaml:"name"`
		NewName string `yaml:"newName"`
		NewTag  string `yaml:"newTag"`
	} `yaml:"images"`
}

type overlay struct {
	path     string // the hosted overlay the workflow rewrites
	base     string // the kustomization the overlay builds on
	image    string // the image name as the base manifests spell it
	registry string // where the workflow publishes it
}

var overlays = []overlay{
	{
		path:     "../../deploy/hosting/operator/kustomization.yaml",
		base:     "../../deploy/operator/kustomization.yaml",
		image:    "redis-operator",
		registry: "ghcr.io/muditjain13/redis-operator",
	},
	{
		path:     "../../deploy/hosting/portal/kustomization.yaml",
		base:     "../../deploy/portal/kustomization.yaml",
		image:    "redis-portal",
		registry: "ghcr.io/muditjain13/redis-portal",
	},
}

func loadKustomization(t *testing.T, path string) (kustomization, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var k kustomization
	if err := yaml.Unmarshal(data, &k); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return k, string(data)
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func TestHostedOverlaysPointAtThePublishedImages(t *testing.T) {
	for _, o := range overlays {
		k, raw := loadKustomization(t, o.path)
		if len(k.Images) != 1 {
			t.Errorf("%s: %d image entries, want exactly 1", o.path, len(k.Images))
			continue
		}
		img := k.Images[0]
		if img.Name != o.image || img.NewName != o.registry {
			t.Errorf("%s: image %q renamed to %q, want %q renamed to %q", o.path, img.Name, img.NewName, o.image, o.registry)
		}
		if !fullSHA.MatchString(img.NewTag) {
			t.Errorf("%s: tag %q is not a full commit SHA", o.path, img.NewTag)
		}
		// Unquoted, an all-digit or 1e5-looking SHA would be read as a number.
		if !strings.Contains(raw, `newTag: "`+img.NewTag+`"`) {
			t.Errorf("%s: the tag must be written in quotes", o.path)
		}
		for _, bad := range []string{"newTag: hosted", "newTag: dev", "newTag: latest", ":latest"} {
			if strings.Contains(raw, bad) {
				t.Errorf("%s still contains %q", o.path, bad)
			}
		}
	}
}

func TestBothHostedOverlaysUseTheSameCommit(t *testing.T) {
	var tags []string
	for _, o := range overlays {
		k, _ := loadKustomization(t, o.path)
		if len(k.Images) == 1 {
			tags = append(tags, k.Images[0].NewTag)
		}
	}
	if len(tags) != 2 || tags[0] != tags[1] {
		t.Errorf("tags = %v; the operator and portal are built from the same commit and must match", tags)
	}
}

func TestOverlayImagesAreTheOnesTheWorkflowPublishes(t *testing.T) {
	published := map[string]bool{}
	for _, m := range mustJob(t, loadWorkflow(t), "images").Strategy.Matrix.Include {
		published[m["image"]] = true
	}
	for _, o := range overlays {
		k, _ := loadKustomization(t, o.path)
		if len(k.Images) != 1 {
			continue
		}
		if !published[k.Images[0].NewName] {
			t.Errorf("%s uses %s, which the workflow does not publish (it publishes %v)", o.path, k.Images[0].NewName, published)
		}
	}
	if len(published) != len(overlays) {
		t.Errorf("the workflow publishes %d images, the overlays use %d", len(published), len(overlays))
	}
}

// The overlay can only replace an image if its name is the name the base
// leaves the image with, so a rename in a base would silently stop the rollout.
func TestOverlayImageNamesMatchTheBases(t *testing.T) {
	for _, o := range overlays {
		base, _ := loadKustomization(t, o.base)
		hosted, _ := loadKustomization(t, o.path)
		if len(base.Images) != 1 || len(hosted.Images) != 1 {
			t.Errorf("%s and %s should each list one image", o.base, o.path)
			continue
		}
		if base.Images[0].NewName != hosted.Images[0].Name {
			t.Errorf("the base leaves the image as %q but the overlay replaces %q",
				base.Images[0].NewName, hosted.Images[0].Name)
		}
	}
}
