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
	"errors"
	"sync"
	"time"
)

// maxHistory is how many finished operations are remembered. The history is
// global, not per visitor: every visitor sees every visitor's actions.
const maxHistory = 20

// ErrBusy means another action is still settling.
var ErrBusy = errors.New("another action is still in progress")

// Tracker times one action at a time, from the click until the cluster has
// settled again, and remembers the most recent results.
type Tracker struct {
	timeout time.Duration
	now     func() time.Time

	mu      sync.Mutex
	current *running
	history []HealRecord // newest first
}

type running struct {
	op   Operation
	done func(State) bool
}

// NewTracker returns a Tracker that gives up on an action after timeout, so a
// cluster that never settles cannot block the portal forever.
func NewTracker(timeout time.Duration) *Tracker {
	return &Tracker{timeout: timeout, now: time.Now}
}

// Begin starts timing action. done decides, from a State snapshot, whether the
// cluster has settled. It returns ErrBusy if an action is already running.
func (t *Tracker) Begin(action Action, done func(State) bool) (Operation, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current != nil {
		return Operation{}, ErrBusy
	}
	op := Operation{Action: action, StartedAt: t.now()}
	t.current = &running{op: op, done: done}
	return op, nil
}

// Abort forgets the running action without recording a result. It is used
// when the action could not be carried out.
func (t *Tracker) Abort() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.current = nil
}

// Observe checks the running action against a new snapshot. It returns the
// finished record if this snapshot completed (or timed out) the action.
func (t *Tracker) Observe(s State) *HealRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return nil
	}

	now := t.now()
	elapsed := now.Sub(t.current.op.StartedAt)
	finished := t.current.done(s)
	if !finished && elapsed < t.timeout {
		return nil
	}

	rec := HealRecord{
		Action:     t.current.op.Action,
		Seconds:    elapsed.Seconds(),
		FinishedAt: now,
		TimedOut:   !finished,
	}
	t.current = nil
	t.history = append([]HealRecord{rec}, t.history...)
	if len(t.history) > maxHistory {
		t.history = t.history[:maxHistory]
	}
	return &rec
}

// InFlight returns the running action, or nil.
func (t *Tracker) InFlight() *Operation {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return nil
	}
	op := t.current.op
	return &op
}

// History returns finished actions, newest first.
func (t *Tracker) History() []HealRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]HealRecord{}, t.history...)
}
