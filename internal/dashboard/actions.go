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
	"fmt"
)

// plan is what an action does and how to tell that the cluster has settled again.
type plan struct {
	do   func(context.Context) error
	done func(State) bool
}

// Perform runs action against the cluster and starts timing how long it takes
// to settle. It returns before the cluster has settled.
func (a *App) Perform(ctx context.Context, action Action) (Operation, error) {
	if _, err := ParseAction(string(action)); err != nil {
		return Operation{}, err
	}

	// Decide from fresh state, not the cached snapshot, so the choice is made
	// on what is true now.
	st, err := a.collector.Refresh(ctx)
	if err != nil {
		return Operation{}, fmt.Errorf("reading cluster state: %w", err)
	}
	if st.Cluster == nil {
		return Operation{}, ErrNoCluster
	}

	p, err := a.plan(action, st)
	if err != nil {
		return Operation{}, err
	}

	// Begin before acting, so two simultaneous requests cannot both proceed.
	op, err := a.tracker.Begin(action, p.done)
	if err != nil {
		return Operation{}, err
	}
	if err := p.do(ctx); err != nil {
		a.tracker.Abort()
		return Operation{}, err
	}
	// Show the in-flight action immediately instead of at the next refresh.
	if _, err := a.collector.Refresh(ctx); err != nil {
		a.logger.Warn("Could not refresh after action", "error", err)
	}
	return op, nil
}

func (a *App) plan(action Action, st State) (plan, error) {
	cluster := *st.Cluster
	b := a.backend

	switch action {
	case ActionKillPod:
		victim, ok := a.pickPod(st.Pods)
		if !ok {
			return plan{}, fmt.Errorf("%w: no running Pod to kill", ErrNoop)
		}
		return plan{
			do: func(ctx context.Context) error { return b.DeletePod(ctx, victim.Name) },
			// Settled once the victim is gone and every Pod is Ready again.
			done: func(s State) bool { return !hasPodUID(s.Pods, victim.UID) && settled(s) },
		}, nil

	case ActionDeleteStatefulSet:
		if cluster.StatefulSetUID == "" {
			return plan{}, fmt.Errorf("%w: the StatefulSet is already gone", ErrNoop)
		}
		oldSTS := cluster.StatefulSetUID
		oldPods := podUIDs(st.Pods)
		return plan{
			do: func(ctx context.Context) error { return b.DeleteStatefulSet(ctx) },
			// Settled once a new StatefulSet exists and all of its Pods are new and Ready.
			done: func(s State) bool {
				return s.Cluster != nil && s.Cluster.StatefulSetUID != "" && s.Cluster.StatefulSetUID != oldSTS &&
					!anyPodUID(s.Pods, oldPods) && settled(s)
			},
		}, nil

	case ActionScaleUp:
		return a.scalePlan(cluster, a.cfg.ScaleUpTo)
	case ActionScaleDown:
		return a.scalePlan(cluster, a.cfg.ScaleDownTo)

	case ActionUpgrade:
		target := a.cfg.UpgradeTo
		if cluster.Version == target {
			return plan{}, fmt.Errorf("%w: already on Redis %s", ErrNoop, target)
		}
		return plan{
			do: func(ctx context.Context) error { return b.SetVersion(ctx, target) },
			done: func(s State) bool {
				return s.Cluster != nil && s.Cluster.Version == target && s.Cluster.CurrentVersion == target &&
					allPodsOnVersion(s.Pods, target) && settled(s)
			},
		}, nil
	}
	return plan{}, ErrUnknownAction
}

func (a *App) scalePlan(cluster ClusterView, target int32) (plan, error) {
	if cluster.Replicas == target {
		return plan{}, fmt.Errorf("%w: already at %d replicas", ErrNoop, target)
	}
	b := a.backend
	return plan{
		do: func(ctx context.Context) error { return b.SetReplicas(ctx, target) },
		done: func(s State) bool {
			return s.Cluster != nil && s.Cluster.Replicas == target && settled(s)
		},
	}, nil
}

// pickPod chooses a random Pod that is not already going away.
func (a *App) pickPod(pods []PodView) (PodView, bool) {
	var candidates []PodView
	for _, p := range pods {
		if p.State != PodTerminating {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return PodView{}, false
	}
	return candidates[a.pick(len(candidates))], true
}

// settled means the operator reports no rollout in progress, and the Pods we
// can see agree: exactly the desired number, all Ready.
func settled(s State) bool {
	c := s.Cluster
	if c == nil || c.ConditionStatus("Progressing") != "False" || c.ReadyReplicas != c.Replicas {
		return false
	}
	if int32(len(s.Pods)) != c.Replicas {
		return false
	}
	for _, p := range s.Pods {
		if p.State != PodReady {
			return false
		}
	}
	return true
}

func hasPodUID(pods []PodView, uid string) bool {
	for _, p := range pods {
		if p.UID == uid {
			return true
		}
	}
	return false
}

func podUIDs(pods []PodView) map[string]bool {
	m := make(map[string]bool, len(pods))
	for _, p := range pods {
		m[p.UID] = true
	}
	return m
}

func anyPodUID(pods []PodView, uids map[string]bool) bool {
	for _, p := range pods {
		if uids[p.UID] {
			return true
		}
	}
	return false
}

func allPodsOnVersion(pods []PodView, version string) bool {
	for _, p := range pods {
		if imageTag(p.Image) != version {
			return false
		}
	}
	return true
}
