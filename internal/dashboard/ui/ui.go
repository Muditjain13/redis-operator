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

// Package ui embeds the built web UI, so that one binary serves both the API
// and the page.
package ui

import (
	"embed"
	"io/fs"
)

// dist holds the output of `make web-build`. A fresh checkout contains only a
// placeholder file, which keeps the Go packages buildable before the UI has
// been built.
//
//go:embed all:dist
var dist embed.FS

// FS returns the built web UI with index.html at its root, or nil if the UI
// has not been built.
func FS() fs.FS {
	return webRoot(dist)
}

func webRoot(embedded fs.FS) fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
