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

import "time"

// newTestApp returns an App around backend with a deterministic Pod choice
// (always the first candidate) and no cooldown surprises.
func newTestApp(backend Backend) *App {
	app := New(Config{Cooldown: 30 * time.Second, HealTimeout: time.Minute}, backend)
	app.pick = func(int) int { return 0 }
	return app
}
