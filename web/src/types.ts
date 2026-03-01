// These types mirror the JSON served by the portal backend
// (internal/dashboard/model.go). Keep the two in sync.

export type PodState = 'Pending' | 'Running' | 'Ready' | 'Terminating' | 'Failed';

export interface Pod {
  name: string;
  uid: string;
  state: PodState;
  image: string;
  node?: string;
  createdAt: string;
}

export interface Condition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface Cluster {
  name: string;
  namespace: string;
  replicas: number;
  version: string;
  readyReplicas: number;
  currentVersion: string;
  statefulSetUID?: string;
  conditions: Condition[];
}

export interface ClusterEvent {
  time: string;
  type: string;
  reason: string;
  message: string;
}

export type ActionName = 'kill-pod' | 'delete-statefulset' | 'scale-up' | 'scale-down' | 'upgrade';

export interface HealRecord {
  action: ActionName;
  seconds: number;
  finishedAt: string;
  timedOut?: boolean;
}

export interface Operation {
  action: ActionName;
  startedAt: string;
}

export interface PortalState {
  cluster: Cluster | null;
  pods: Pod[];
  events: ClusterEvent[];
  heals: HealRecord[];
  inFlight: Operation | null;
}

export type ConnectionStatus = 'connecting' | 'live' | 'reconnecting';
