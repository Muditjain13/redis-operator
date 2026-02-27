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
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type collectorFixture struct {
	fb        *fakeBackend
	tracker   *Tracker
	hub       *Hub
	collector *Collector
	msgs      <-chan Message
}

func newCollectorFixture(t *testing.T) *collectorFixture {
	t.Helper()
	fb := newFakeBackend()
	tracker := NewTracker(time.Minute)
	hub := NewHub(10)
	msgs, cancel, err := hub.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	return &collectorFixture{
		fb: fb, tracker: tracker, hub: hub, msgs: msgs,
		collector: NewCollector(fb, tracker, hub, 20*time.Millisecond),
	}
}

func (f *collectorFixture) drain() []Message {
	var out []Message
	for {
		select {
		case m := <-f.msgs:
			out = append(out, m)
		default:
			return out
		}
	}
}

func messageTypes(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Type)
	}
	return out
}

func TestRefreshPublishesStateOnlyWhenItChanges(t *testing.T) {
	f := newCollectorFixture(t)
	ctx := context.Background()

	if _, err := f.collector.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := messageTypes(f.drain()); len(got) != 1 || got[0] != "state" {
		t.Fatalf("first refresh published %v, want one state message", got)
	}

	if _, err := f.collector.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.drain(); len(got) != 0 {
		t.Fatalf("an unchanged refresh must publish nothing, got %v", messageTypes(got))
	}

	f.fb.update(func(b *fakeBackend) { b.pods[1].State = PodTerminating })
	if _, err := f.collector.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := messageTypes(f.drain()); len(got) != 1 || got[0] != "state" {
		t.Fatalf("a changed refresh published %v, want one state message", got)
	}
}

func TestRefreshSortsPodsAndHandlesAMissingCluster(t *testing.T) {
	f := newCollectorFixture(t)
	f.fb.update(func(b *fakeBackend) {
		b.cluster = nil
		b.pods = []PodView{{Name: "x-2"}, {Name: "x-0"}}
	})
	st, err := f.collector.Refresh(context.Background())
	if err != nil {
		t.Fatalf("a missing cluster is a state, not an error: %v", err)
	}
	if st.Cluster != nil || st.Pods[0].Name != "x-0" {
		t.Fatalf("state = %+v", st)
	}
	if f.collector.Snapshot().Cluster != nil {
		t.Fatal("Snapshot must reflect the refresh")
	}
}

func TestRefreshErrors(t *testing.T) {
	f := newCollectorFixture(t)
	ctx := context.Background()

	f.fb.update(func(b *fakeBackend) { b.clusterErr = errors.New("boom") })
	if _, err := f.collector.Refresh(ctx); err == nil {
		t.Fatal("a cluster read error must be returned")
	}

	f.fb.update(func(b *fakeBackend) { b.clusterErr = nil; b.podsErr = errors.New("boom") })
	if _, err := f.collector.Refresh(ctx); err == nil {
		t.Fatal("a Pod list error must be returned")
	}
}

func TestRefreshPublishesAHealWhenTheActionSettles(t *testing.T) {
	f := newCollectorFixture(t)
	done := false
	if _, err := f.tracker.Begin(ActionKillPod, func(State) bool { return done }); err != nil {
		t.Fatal(err)
	}

	if _, err := f.collector.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.drain()

	done = true
	st, err := f.collector.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Heals) != 1 || st.InFlight != nil {
		t.Fatalf("state = %+v", st)
	}
	got := messageTypes(f.drain())
	if len(got) != 2 || got[0] != "heal" || got[1] != "state" {
		t.Fatalf("published %v, want heal then state", got)
	}
}

func TestAddEventDeduplicates(t *testing.T) {
	c := NewCollector(newFakeBackend(), NewTracker(time.Minute), NewHub(1), time.Second)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev := EventView{Time: at, Type: "Normal", Reason: "Healed", Message: "Recreated"}

	if !c.addEvent(ev) {
		t.Fatal("the first event is new")
	}
	// The same instant expressed in another zone is still the same event.
	same := ev
	same.Time = at.In(time.FixedZone("x", 3600))
	if c.addEvent(same) {
		t.Fatal("a repeated event must be ignored")
	}

	for i := range maxEvents + 5 {
		c.addEvent(EventView{Time: at.Add(time.Duration(i+1) * time.Second), Reason: "R", Message: "m"})
	}
	if got := len(c.events); got != maxEvents {
		t.Fatalf("kept %d events, want %d", got, maxEvents)
	}
}

func TestRunStreamsPodAndEventChanges(t *testing.T) {
	f := newCollectorFixture(t)
	f.fb.update(func(b *fakeBackend) {
		b.events = []EventView{{Time: time.Now().Add(-time.Hour), Type: "Normal", Reason: "Created", Message: "Created StatefulSet"}}
	})

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		f.collector.Run(ctx)
		close(finished)
	}()

	waitFor := func(msgType string) Message {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case m := <-f.msgs:
				if m.Type == msgType {
					return m
				}
			case <-timeout:
				t.Fatalf("no %q message", msgType)
			}
		}
	}

	// Events that existed before the portal started are in the first state.
	first := waitFor("state")
	if !strings.Contains(string(first.Data), "Created StatefulSet") {
		t.Fatalf("initial events missing from the first state: %s", first.Data)
	}

	f.fb.podWatch <- PodChange{Type: "MODIFIED", Pod: PodView{Name: podName(0), State: PodTerminating}}
	if m := waitFor("pod"); !strings.Contains(string(m.Data), "MODIFIED") {
		t.Fatalf("pod message = %s", m.Data)
	}

	ev := EventView{Time: time.Now(), Type: "Normal", Reason: "Healed", Message: "Recreated missing StatefulSet"}
	f.fb.eventWatch <- ev
	if m := waitFor("event"); !strings.Contains(string(m.Data), "Recreated missing StatefulSet") {
		t.Fatalf("event message = %s", m.Data)
	}
	// Replaying the same event (a watch reconnect) must not publish it again.
	f.fb.eventWatch <- ev

	cancel()
	close(f.fb.podWatch)
	close(f.fb.eventWatch)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when the context ended")
	}
}

func TestWatchLoopReopensAnEndedWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opens := 0
	got := make(chan int, 1)
	open := func(context.Context) (<-chan int, error) {
		opens++
		ch := make(chan int, 1)
		switch opens {
		case 1:
			return nil, errors.New("api unavailable") // fails, then retried
		case 2:
			close(ch) // an ordinary ended watch, then retried
		default:
			ch <- 42
			close(ch)
		}
		return ch, nil
	}

	finished := make(chan struct{})
	go func() {
		watchLoop(ctx, "test", open, func(v int) {
			got <- v
			cancel()
		})
		close(finished)
	}()

	select {
	case v := <-got:
		if v != 42 || opens != 3 {
			t.Fatalf("got %d after %d opens", v, opens)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watch was not reopened")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not stop when the context ended")
	}
}
