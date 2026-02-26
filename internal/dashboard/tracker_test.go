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
	"testing"
	"time"
)

func TestTrackerTimesAnActionUntilItSettles(t *testing.T) {
	clock := newClock()
	tr := NewTracker(time.Minute)
	tr.now = clock.now

	settled := false
	op, err := tr.Begin(ActionKillPod, func(State) bool { return settled })
	if err != nil {
		t.Fatal(err)
	}
	if op.Action != ActionKillPod || !op.StartedAt.Equal(clock.now()) {
		t.Fatalf("unexpected operation: %+v", op)
	}
	if got := tr.InFlight(); got == nil || got.Action != ActionKillPod {
		t.Fatalf("InFlight = %+v", got)
	}

	clock.advance(12 * time.Second)
	if rec := tr.Observe(State{}); rec != nil {
		t.Fatalf("not settled yet, but got a record: %+v", rec)
	}

	settled = true
	clock.advance(3 * time.Second)
	rec := tr.Observe(State{})
	if rec == nil {
		t.Fatal("expected a record once settled")
	}
	if rec.Seconds != 15 || rec.TimedOut || rec.Action != ActionKillPod {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if tr.InFlight() != nil {
		t.Fatal("nothing should be in flight after settling")
	}
	if h := tr.History(); len(h) != 1 || h[0] != *rec {
		t.Fatalf("history = %+v", h)
	}
	if tr.Observe(State{}) != nil {
		t.Fatal("observing with nothing in flight must be a no-op")
	}
}

func TestTrackerAllowsOneActionAtATime(t *testing.T) {
	tr := NewTracker(time.Minute)
	never := func(State) bool { return false }
	if _, err := tr.Begin(ActionScaleUp, never); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Begin(ActionUpgrade, never); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	tr.Abort()
	if _, err := tr.Begin(ActionUpgrade, never); err != nil {
		t.Fatalf("after Abort a new action must be allowed: %v", err)
	}
	if len(tr.History()) != 0 {
		t.Fatal("an aborted action must not be recorded")
	}
}

func TestTrackerTimesOut(t *testing.T) {
	clock := newClock()
	tr := NewTracker(time.Minute)
	tr.now = clock.now
	if _, err := tr.Begin(ActionUpgrade, func(State) bool { return false }); err != nil {
		t.Fatal(err)
	}

	clock.advance(30 * time.Second)
	if tr.Observe(State{}) != nil {
		t.Fatal("must not time out early")
	}
	clock.advance(31 * time.Second)
	rec := tr.Observe(State{})
	if rec == nil || !rec.TimedOut || rec.Seconds != 61 {
		t.Fatalf("expected a timed-out record, got %+v", rec)
	}
	if tr.InFlight() != nil {
		t.Fatal("a timed-out action must free the tracker")
	}
}

func TestTrackerKeepsNewestFirstAndCapsHistory(t *testing.T) {
	clock := newClock()
	tr := NewTracker(time.Minute)
	tr.now = clock.now

	for range maxHistory + 5 {
		if _, err := tr.Begin(ActionScaleUp, func(State) bool { return true }); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Second)
		tr.Observe(State{})
	}
	h := tr.History()
	if len(h) != maxHistory {
		t.Fatalf("history length = %d, want %d", len(h), maxHistory)
	}
	if !h[0].FinishedAt.After(h[1].FinishedAt) {
		t.Fatal("history must be newest first")
	}
}

func TestHubDeliversAndDropsForSlowSubscribers(t *testing.T) {
	hub := NewHub(2)
	a, cancelA, err := hub.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	b, cancelB, err := hub.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := hub.Subscribe(); !errors.Is(err, ErrTooManySubscribers) {
		t.Fatalf("err = %v, want ErrTooManySubscribers", err)
	}
	if hub.Subscribers() != 2 {
		t.Fatalf("subscribers = %d", hub.Subscribers())
	}

	hub.Publish(Message{Type: "state", Data: []byte("1")})
	if m := <-a; m.Type != "state" || string(m.Data) != "1" {
		t.Fatalf("a got %+v", m)
	}

	// b never reads. Publishing more than its buffer must not block.
	for range subscriberBuffer + 10 {
		hub.Publish(Message{Type: "state"})
	}
	if len(b) != subscriberBuffer {
		t.Fatalf("slow subscriber buffer = %d, want %d", len(b), subscriberBuffer)
	}

	cancelA()
	cancelB()
	if hub.Subscribers() != 0 {
		t.Fatalf("subscribers after cancel = %d", hub.Subscribers())
	}
}
