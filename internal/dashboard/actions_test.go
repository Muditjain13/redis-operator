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
	"reflect"
	"testing"
	"time"
)

// actionFixture is an App on a healthy 3-replica cluster with a fake clock.
type actionFixture struct {
	app   *App
	fb    *fakeBackend
	clock *fakeClock
}

func newActionFixture(t *testing.T) *actionFixture {
	t.Helper()
	fb := newFakeBackend()
	app := newTestApp(fb)
	clock := newClock()
	app.tracker.now = clock.now
	return &actionFixture{app: app, fb: fb, clock: clock}
}

func (f *actionFixture) perform(action Action) (Operation, error) {
	return f.app.Perform(context.Background(), action)
}

func (f *actionFixture) refresh(t *testing.T) State {
	t.Helper()
	st, err := f.app.collector.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestParseAction(t *testing.T) {
	for _, name := range []string{"kill-pod", "delete-statefulset", "scale-up", "scale-down", "upgrade"} {
		if a, err := ParseAction(name); err != nil || string(a) != name {
			t.Errorf("ParseAction(%q) = %q, %v", name, a, err)
		}
	}
	for _, name := range []string{"", "KILL-POD", "kill-pod ", "../x", "drop-database"} {
		if _, err := ParseAction(name); !errors.Is(err, ErrUnknownAction) {
			t.Errorf("ParseAction(%q) err = %v, want ErrUnknownAction", name, err)
		}
	}
}

func TestPerformKillPodTimesTheHeal(t *testing.T) {
	f := newActionFixture(t)

	op, err := f.perform(ActionKillPod)
	if err != nil {
		t.Fatal(err)
	}
	if op.Action != ActionKillPod {
		t.Fatalf("op = %+v", op)
	}
	if want := []string{podName(0)}; !reflect.DeepEqual(f.fb.deleted, want) {
		t.Fatalf("deleted = %v, want %v", f.fb.deleted, want)
	}
	if st := f.app.collector.Snapshot(); st.InFlight == nil || st.InFlight.Action != ActionKillPod {
		t.Fatalf("the action must be visible as in flight, got %+v", st.InFlight)
	}

	// The victim is still around, now terminating: not healed.
	f.clock.advance(2 * time.Second)
	f.fb.update(func(b *fakeBackend) { b.pods[0].State = PodTerminating })
	if st := f.refresh(t); len(st.Heals) != 0 {
		t.Fatal("must not report a heal while the victim still exists")
	}

	// The victim is gone and its replacement is not Ready yet: not healed.
	f.clock.advance(3 * time.Second)
	f.fb.update(func(b *fakeBackend) {
		b.pods = []PodView{b.pods[1], b.pods[2], {Name: podName(0), UID: "uid-new", State: PodPending, Image: "redis:7.2"}}
	})
	if st := f.refresh(t); len(st.Heals) != 0 {
		t.Fatal("must not report a heal before the replacement is Ready")
	}

	// The replacement is Ready: healed, 15 seconds after the click.
	f.clock.advance(10 * time.Second)
	f.fb.update(func(b *fakeBackend) { b.pods[2].State = PodReady })
	st := f.refresh(t)
	if len(st.Heals) != 1 || st.Heals[0].Seconds != 15 || st.Heals[0].Action != ActionKillPod || st.Heals[0].TimedOut {
		t.Fatalf("heals = %+v", st.Heals)
	}
	if st.InFlight != nil {
		t.Fatal("nothing should be in flight after the heal")
	}
}

func TestPerformDeleteStatefulSetWaitsForNewObjectAndNewPods(t *testing.T) {
	f := newActionFixture(t)
	if _, err := f.perform(ActionDeleteStatefulSet); err != nil {
		t.Fatal(err)
	}
	if f.fb.stsDelete != 1 {
		t.Fatal("the StatefulSet must have been deleted")
	}

	// A new StatefulSet exists but the old Pods are still there: not healed.
	f.clock.advance(time.Second)
	f.fb.update(func(b *fakeBackend) { b.cluster.StatefulSetUID = "sts-2" })
	if st := f.refresh(t); len(st.Heals) != 0 {
		t.Fatal("old Pods still present: must not be healed")
	}

	// New StatefulSet, new Ready Pods: healed.
	f.clock.advance(39 * time.Second)
	f.fb.update(func(b *fakeBackend) {
		for i := range b.pods {
			b.pods[i].UID = "new-" + b.pods[i].UID
		}
	})
	st := f.refresh(t)
	if len(st.Heals) != 1 || st.Heals[0].Seconds != 40 {
		t.Fatalf("heals = %+v", st.Heals)
	}
}

func TestPerformScale(t *testing.T) {
	f := newActionFixture(t)
	if _, err := f.perform(ActionScaleUp); err != nil {
		t.Fatal(err)
	}
	if want := []int32{5}; !reflect.DeepEqual(f.fb.replicas, want) {
		t.Fatalf("replicas set = %v, want %v", f.fb.replicas, want)
	}

	// The operator accepts the change but only 3 Pods are Ready: not done.
	f.fb.update(func(b *fakeBackend) { b.cluster.Replicas = 5 })
	if st := f.refresh(t); len(st.Heals) != 0 {
		t.Fatal("2 Pods are missing: must not be done")
	}

	f.clock.advance(25 * time.Second)
	f.fb.update(func(b *fakeBackend) {
		b.cluster = settledCluster(5)
		b.pods = readyPods(5)
	})
	st := f.refresh(t)
	if len(st.Heals) != 1 || st.Heals[0].Seconds != 25 || st.Heals[0].Action != ActionScaleUp {
		t.Fatalf("heals = %+v", st.Heals)
	}

	// And back down to 3.
	if _, err := f.perform(ActionScaleDown); err != nil {
		t.Fatal(err)
	}
	if want := []int32{5, 3}; !reflect.DeepEqual(f.fb.replicas, want) {
		t.Fatalf("replicas set = %v, want %v", f.fb.replicas, want)
	}
}

func TestPerformUpgrade(t *testing.T) {
	f := newActionFixture(t)
	if _, err := f.perform(ActionUpgrade); err != nil {
		t.Fatal(err)
	}
	if want := []string{"7.4"}; !reflect.DeepEqual(f.fb.versions, want) {
		t.Fatalf("versions set = %v, want %v", f.fb.versions, want)
	}

	// Spec says 7.4 but the rollout has not finished: currentVersion is still 7.2.
	f.fb.update(func(b *fakeBackend) { b.cluster.Version = "7.4" })
	if st := f.refresh(t); len(st.Heals) != 0 {
		t.Fatal("rollout has not finished: must not be done")
	}

	// currentVersion advanced but one Pod still runs the old image: not done.
	f.fb.update(func(b *fakeBackend) {
		b.cluster.CurrentVersion = "7.4"
		b.pods[0].Image = "redis:7.4"
		b.pods[1].Image = "redis:7.4"
	})
	if st := f.refresh(t); len(st.Heals) != 0 {
		t.Fatal("a Pod still runs 7.2: must not be done")
	}

	f.clock.advance(52 * time.Second)
	f.fb.update(func(b *fakeBackend) { b.pods[2].Image = "redis:7.4" })
	st := f.refresh(t)
	if len(st.Heals) != 1 || st.Heals[0].Seconds != 52 {
		t.Fatalf("heals = %+v", st.Heals)
	}
}

func TestPerformRefusesActionsThatWouldDoNothing(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		mutate func(*fakeBackend)
	}{
		{"scale up when already there", ActionScaleUp, func(b *fakeBackend) { b.cluster.Replicas = 5 }},
		{"scale down when already there", ActionScaleDown, func(b *fakeBackend) {}},
		{"upgrade when already upgraded", ActionUpgrade, func(b *fakeBackend) { b.cluster.Version = "7.4" }},
		{"delete a StatefulSet that is gone", ActionDeleteStatefulSet, func(b *fakeBackend) { b.cluster.StatefulSetUID = "" }},
		{"kill a Pod when there are none", ActionKillPod, func(b *fakeBackend) { b.pods = nil }},
		{"kill a Pod when all are terminating", ActionKillPod, func(b *fakeBackend) {
			for i := range b.pods {
				b.pods[i].State = PodTerminating
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newActionFixture(t)
			f.fb.update(tt.mutate)

			_, err := f.perform(tt.action)
			if !errors.Is(err, ErrNoop) {
				t.Fatalf("err = %v, want ErrNoop", err)
			}
			if f.app.tracker.InFlight() != nil {
				t.Fatal("a refused action must not leave anything in flight")
			}
			if len(f.fb.deleted)+f.fb.stsDelete+len(f.fb.replicas)+len(f.fb.versions) != 0 {
				t.Fatal("a refused action must not touch the cluster")
			}
		})
	}
}

func TestPerformErrors(t *testing.T) {
	t.Run("unknown action", func(t *testing.T) {
		f := newActionFixture(t)
		if _, err := f.perform("explode"); !errors.Is(err, ErrUnknownAction) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("no cluster", func(t *testing.T) {
		f := newActionFixture(t)
		f.fb.update(func(b *fakeBackend) { b.cluster = nil })
		if _, err := f.perform(ActionKillPod); !errors.Is(err, ErrNoCluster) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("cannot read state", func(t *testing.T) {
		f := newActionFixture(t)
		f.fb.update(func(b *fakeBackend) { b.podsErr = errors.New("api down") })
		if _, err := f.perform(ActionKillPod); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("another action is running", func(t *testing.T) {
		f := newActionFixture(t)
		if _, err := f.perform(ActionKillPod); err != nil {
			t.Fatal(err)
		}
		if _, err := f.perform(ActionScaleUp); !errors.Is(err, ErrBusy) {
			t.Fatalf("err = %v, want ErrBusy", err)
		}
		if len(f.fb.replicas) != 0 {
			t.Fatal("a refused action must not touch the cluster")
		}
	})

	t.Run("the cluster call fails", func(t *testing.T) {
		f := newActionFixture(t)
		f.fb.update(func(b *fakeBackend) { b.actionErr = errors.New("forbidden") })
		if _, err := f.perform(ActionScaleUp); err == nil {
			t.Fatal("expected the backend error")
		}
		if f.app.tracker.InFlight() != nil {
			t.Fatal("a failed action must be aborted so the next one can run")
		}
		f.fb.update(func(b *fakeBackend) { b.actionErr = nil })
		if _, err := f.perform(ActionScaleUp); err != nil {
			t.Fatalf("the next action must be allowed: %v", err)
		}
	})
}

func TestPickPodSkipsTerminating(t *testing.T) {
	app := newTestApp(newFakeBackend())
	pods := []PodView{
		{Name: "a", State: PodTerminating},
		{Name: "b", State: PodReady},
		{Name: "c", State: PodReady},
	}
	app.pick = func(n int) int { return n - 1 } // the last candidate
	got, ok := app.pickPod(pods)
	if !ok || got.Name != "c" {
		t.Fatalf("picked %+v ok=%v, want c", got, ok)
	}
	if _, ok := app.pickPod(pods[:1]); ok {
		t.Fatal("nothing to pick when everything is terminating")
	}
}

func TestSettled(t *testing.T) {
	healthy := func() State { return State{Cluster: settledCluster(3), Pods: readyPods(3)} }
	tests := []struct {
		name   string
		mutate func(*State)
		want   bool
	}{
		{"healthy", func(*State) {}, true},
		{"no cluster", func(s *State) { s.Cluster = nil }, false},
		{"operator still progressing", func(s *State) {
			s.Cluster.Conditions = []ConditionView{{Type: "Progressing", Status: "True"}}
		}, false},
		{"condition not reported yet", func(s *State) { s.Cluster.Conditions = nil }, false},
		{"operator sees fewer ready", func(s *State) { s.Cluster.ReadyReplicas = 2 }, false},
		{"a Pod is missing", func(s *State) { s.Pods = s.Pods[:2] }, false},
		{"an extra Pod", func(s *State) { s.Pods = append(s.Pods, PodView{Name: "x", State: PodReady}) }, false},
		{"a Pod is not Ready", func(s *State) { s.Pods[1].State = PodRunning }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := healthy()
			tt.mutate(&s)
			if got := settled(s); got != tt.want {
				t.Fatalf("settled = %v, want %v", got, tt.want)
			}
		})
	}
}
