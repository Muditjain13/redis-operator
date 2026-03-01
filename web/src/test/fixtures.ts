import type { Cluster, ClusterEvent, HealRecord, Pod, PortalState } from '../types';

export function makePod(overrides: Partial<Pod> = {}): Pod {
  return {
    uid: 'uid-0',
    name: 'rediscluster-sample-0',
    state: 'Ready',
    image: 'redis:7.2',
    node: 'kind-control-plane',
    createdAt: '2026-01-01T12:00:00Z',
    ...overrides,
  };
}

export function makeCluster(overrides: Partial<Cluster> = {}): Cluster {
  return {
    name: 'rediscluster-sample',
    namespace: 'demo',
    replicas: 3,
    version: '7.2',
    readyReplicas: 3,
    currentVersion: '7.2',
    conditions: [
      { type: 'Available', status: 'True' },
      { type: 'Progressing', status: 'False' },
      { type: 'Degraded', status: 'False' },
    ],
    ...overrides,
  };
}

export function makeEvent(overrides: Partial<ClusterEvent> = {}): ClusterEvent {
  return {
    time: '2026-01-01T12:00:05Z',
    type: 'Normal',
    reason: 'Healed',
    message: 'Recreated missing StatefulSet rediscluster-sample',
    ...overrides,
  };
}

export function makeHeal(overrides: Partial<HealRecord> = {}): HealRecord {
  return { action: 'kill-pod', seconds: 12.34, finishedAt: '2026-01-01T12:00:30Z', ...overrides };
}

export function makeState(overrides: Partial<PortalState> = {}): PortalState {
  return {
    cluster: makeCluster(),
    pods: [0, 1, 2].map((i) => makePod({ uid: `uid-${i}`, name: `rediscluster-sample-${i}` })),
    events: [],
    heals: [],
    inFlight: null,
    ...overrides,
  };
}
