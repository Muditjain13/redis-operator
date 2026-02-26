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

import "errors"

// Action is one of the fixed things a visitor can do. There is no free-text
// input anywhere: the target of every action is chosen by the server.
type Action string

// The actions the portal offers.
const (
	ActionKillPod           Action = "kill-pod"
	ActionDeleteStatefulSet Action = "delete-statefulset"
	ActionScaleUp           Action = "scale-up"
	ActionScaleDown         Action = "scale-down"
	ActionUpgrade           Action = "upgrade"
)

// Errors returned when an action cannot run.
var (
	ErrUnknownAction = errors.New("unknown action")
	ErrNoCluster     = errors.New("the RedisCluster does not exist")
	// ErrNoop means the cluster is already in the state the action would
	// produce, or has nothing the action could act on.
	ErrNoop = errors.New("nothing to do")
)

// ParseAction validates a name taken from a URL.
func ParseAction(name string) (Action, error) {
	switch a := Action(name); a {
	case ActionKillPod, ActionDeleteStatefulSet, ActionScaleUp, ActionScaleDown, ActionUpgrade:
		return a, nil
	}
	return "", ErrUnknownAction
}
