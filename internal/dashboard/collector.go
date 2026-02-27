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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// maxEvents is how many operator events are kept for the feed.
const maxEvents = 30

// Collector keeps the latest State, feeds the Tracker, and tells the Hub when
// something changed. Pod and event changes arrive through the watch API; a
// slow periodic refresh covers what watches do not deliver (the RedisCluster
// status) and heals any watch that silently stalls.
type Collector struct {
	backend  Backend
	tracker  *Tracker
	hub      *Hub
	interval time.Duration

	mu       sync.Mutex
	state    State
	lastJSON []byte
	events   []EventView
}

// NewCollector returns a Collector that refreshes at least every interval.
func NewCollector(backend Backend, tracker *Tracker, hub *Hub, interval time.Duration) *Collector {
	return &Collector{
		backend:  backend,
		tracker:  tracker,
		hub:      hub,
		interval: interval,
		state:    State{Pods: []PodView{}, Events: []EventView{}, Heals: []HealRecord{}},
	}
}

// Snapshot returns the latest State.
func (c *Collector) Snapshot() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Refresh reads the cluster and its Pods, lets the Tracker see them, and
// publishes the new State if it differs from the last one.
func (c *Collector) Refresh(ctx context.Context) (State, error) {
	cluster, err := c.backend.GetCluster(ctx)
	if err != nil && !errors.Is(err, ErrClusterNotFound) {
		return State{}, err
	}
	pods, err := c.backend.ListPods(ctx)
	if err != nil {
		return State{}, err
	}
	sortPods(pods)

	c.mu.Lock()
	st := State{Cluster: cluster, Pods: pods, Events: append([]EventView{}, c.events...)}
	c.mu.Unlock()

	rec := c.tracker.Observe(st)
	st.Heals = c.tracker.History()
	st.InFlight = c.tracker.InFlight()

	c.mu.Lock()
	c.state = st
	c.mu.Unlock()

	if rec != nil {
		c.publish("heal", rec)
	}
	c.publishStateIfChanged(st)
	return st, nil
}

// Run watches Pods and events and refreshes periodically until ctx ends.
func (c *Collector) Run(ctx context.Context) {
	c.loadEvents(ctx)
	c.refreshLogged(ctx)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		watchLoop(ctx, "pods", c.backend.WatchPods, func(ch PodChange) {
			c.publish("pod", ch)
			c.refreshLogged(ctx)
		})
	}()
	go func() {
		defer wg.Done()
		watchLoop(ctx, "events", c.backend.WatchEvents, func(ev EventView) {
			if c.addEvent(ev) {
				c.publish("event", ev)
				c.refreshLogged(ctx)
			}
		})
	}()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-ticker.C:
			c.refreshLogged(ctx)
		}
	}
}

func (c *Collector) loadEvents(ctx context.Context) {
	events, err := c.backend.ListEvents(ctx, maxEvents)
	if err != nil {
		slog.Warn("Could not list events", "error", err)
		return
	}
	c.mu.Lock()
	c.events = events
	c.mu.Unlock()
}

// addEvent records ev and reports whether it was new. Watches replay existing
// events on (re)connect, so duplicates are expected.
func (c *Collector) addEvent(ev EventView) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, have := range c.events {
		// Compare the instant, not the struct: the same time parsed twice can
		// carry different location pointers and still be equal.
		if have.Time.Equal(ev.Time) && have.Type == ev.Type && have.Reason == ev.Reason && have.Message == ev.Message {
			return false
		}
	}
	c.events = append(c.events, ev)
	if len(c.events) > maxEvents {
		c.events = c.events[len(c.events)-maxEvents:]
	}
	return true
}

func (c *Collector) refreshLogged(ctx context.Context) {
	if _, err := c.Refresh(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("Could not refresh state", "error", err)
	}
}

func (c *Collector) publish(msgType string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("Could not encode message", "type", msgType, "error", err)
		return
	}
	c.hub.Publish(Message{Type: msgType, Data: data})
}

func (c *Collector) publishStateIfChanged(st State) {
	data, err := json.Marshal(st)
	if err != nil {
		slog.Error("Could not encode state", "error", err)
		return
	}
	c.mu.Lock()
	same := bytes.Equal(data, c.lastJSON)
	c.lastJSON = data
	c.mu.Unlock()
	if !same {
		c.hub.Publish(Message{Type: "state", Data: data})
	}
}

// watchLoop runs a watch, delivering each item to handle, and reopens it with
// a growing delay when it ends or fails. API server watches are routinely
// closed after a few minutes, so ending is normal and not an error.
func watchLoop[T any](ctx context.Context, name string, open func(context.Context) (<-chan T, error), handle func(T)) {
	const maxDelay = 10 * time.Second
	delay := time.Second
	for ctx.Err() == nil {
		ch, err := open(ctx)
		if err != nil {
			slog.Warn("Could not open watch", "watch", name, "error", err)
		} else {
			delay = time.Second
			for item := range ch {
				handle(item)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, maxDelay)
	}
}
