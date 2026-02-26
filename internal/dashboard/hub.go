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
)

// subscriberBuffer is how many messages a slow browser may fall behind by
// before messages to it are dropped.
const subscriberBuffer = 32

// ErrTooManySubscribers means the stream limit has been reached.
var ErrTooManySubscribers = errors.New("too many open streams")

// Message is one Server-Sent Event: its name and a JSON payload.
type Message struct {
	Type string
	Data []byte
}

// Hub fans messages out to every connected browser. Publishing never blocks:
// a subscriber that cannot keep up simply misses messages, which is safe
// because each "state" message is a full snapshot that supersedes the last.
type Hub struct {
	max int

	mu   sync.Mutex
	subs map[chan Message]struct{}
}

// NewHub returns a Hub that allows at most max concurrent subscribers.
func NewHub(max int) *Hub {
	return &Hub{max: max, subs: map[chan Message]struct{}{}}
}

// Subscribe registers a subscriber. The returned function unregisters it and
// must be called exactly when the subscriber goes away.
func (h *Hub) Subscribe() (<-chan Message, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= h.max {
		return nil, nil, ErrTooManySubscribers
	}
	ch := make(chan Message, subscriberBuffer)
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs, ch)
	}, nil
}

// Publish sends m to every subscriber that has room for it.
func (h *Hub) Publish(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
}

// Subscribers returns the number of connected subscribers.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
