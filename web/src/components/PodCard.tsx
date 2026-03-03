import { formatAge } from '../format';
import type { Pod } from '../types';

interface Props {
  pod: Pod;
  now: number;
}

export function PodCard({ pod, now }: Props) {
  return (
    <article className="pod" data-state={pod.state} aria-label={`${pod.name}, ${pod.state}`}>
      <header className="pod-header">
        <h3 className="pod-name">{pod.name}</h3>
        <span className="badge" data-state={pod.state}>
          <span className="dot" aria-hidden="true" />
          {pod.state}
        </span>
      </header>
      <dl className="pod-facts">
        <div>
          <dt>Image</dt>
          <dd>{pod.image || '-'}</dd>
        </div>
        <div>
          <dt>Node</dt>
          <dd>{pod.node || '-'}</dd>
        </div>
        <div>
          <dt>Age</dt>
          <dd>{formatAge(pod.createdAt, now)}</dd>
        </div>
      </dl>
    </article>
  );
}
