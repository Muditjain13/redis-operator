import type { Pod } from '../types';
import { PodCard } from './PodCard';

interface Props {
  pods: Pod[];
  /** How many Pods the cluster should have; missing ones are shown as empty slots. */
  desired: number;
  now: number;
}

export function PodGrid({ pods, desired, now }: Props) {
  const missing = Math.max(0, desired - pods.length);

  return (
    <section aria-labelledby="pods-heading">
      <h2 id="pods-heading">Pods</h2>
      <div className="pod-grid">
        {pods.map((pod) => (
          <PodCard key={pod.uid || pod.name} pod={pod} now={now} />
        ))}
        {Array.from({ length: missing }, (_, i) => (
          <div key={`missing-${i}`} className="pod pod-missing" role="status">
            Waiting for a Pod…
          </div>
        ))}
      </div>
      {pods.length === 0 && desired === 0 && <p className="muted">There are no Pods.</p>}
    </section>
  );
}
