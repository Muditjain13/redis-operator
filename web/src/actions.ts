import type { ActionName } from './types';

export interface ActionInfo {
  name: ActionName;
  label: string;
  hint: string;
  /** Who repairs the cluster afterwards. Worth saying out loud: not everything is the operator. */
  healedBy: 'Kubernetes' | 'the operator';
}

export const ACTIONS: readonly ActionInfo[] = [
  {
    name: 'kill-pod',
    label: 'Kill a random Pod',
    hint: 'Deletes one Redis Pod. The StatefulSet controller replaces it.',
    healedBy: 'Kubernetes',
  },
  {
    name: 'delete-statefulset',
    label: 'Delete the StatefulSet',
    hint: 'Removes the StatefulSet and its Pods. The operator recreates them.',
    healedBy: 'the operator',
  },
  {
    name: 'scale-up',
    label: 'Scale up to 5',
    hint: 'Changes spec.replicas. The operator updates the StatefulSet.',
    healedBy: 'the operator',
  },
  {
    name: 'scale-down',
    label: 'Scale back to 3',
    hint: 'Changes spec.replicas back to 3.',
    healedBy: 'the operator',
  },
  {
    name: 'upgrade',
    label: 'Upgrade to Redis 7.4',
    hint: 'Changes spec.version. Pods are replaced one at a time.',
    healedBy: 'the operator',
  },
];

export function actionLabel(name: ActionName): string {
  return ACTIONS.find((a) => a.name === name)?.label ?? name;
}

/** The cooldown the server enforces between one visitor's actions, mirrored here for the countdown. */
export const COOLDOWN_SECONDS = 30;
