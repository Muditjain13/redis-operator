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

package ui

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestWebRootServesABuiltUI(t *testing.T) {
	root := webRoot(fstest.MapFS{
		"dist/index.html":      {Data: []byte("<html></html>")},
		"dist/assets/app-1.js": {Data: []byte("1")},
	})
	if root == nil {
		t.Fatal("a built UI must be served")
	}
	if _, err := fs.Stat(root, "index.html"); err != nil {
		t.Fatalf("index.html must be at the root: %v", err)
	}
	if _, err := fs.Stat(root, "assets/app-1.js"); err != nil {
		t.Fatalf("assets must be reachable: %v", err)
	}
}

func TestWebRootIsNilUntilTheUIIsBuilt(t *testing.T) {
	if webRoot(fstest.MapFS{"dist/.gitkeep": {}}) != nil {
		t.Fatal("a placeholder-only dist must not count as a UI")
	}
	if webRoot(fstest.MapFS{}) != nil {
		t.Fatal("a missing dist must not count as a UI")
	}
}

func TestFSMatchesWhatWasEmbedded(t *testing.T) {
	// Whether or not the UI was built before this test, FS must be consistent:
	// nil, or a filesystem with index.html at the root.
	if root := FS(); root != nil {
		if _, err := fs.Stat(root, "index.html"); err != nil {
			t.Fatalf("FS() returned a root without index.html: %v", err)
		}
	}
}
