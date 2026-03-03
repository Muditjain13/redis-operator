import type { Cluster } from '../types';

interface Props {
  cluster: Cluster;
}

// How each condition should look when it is True. Degraded is the odd one out:
// True means something is wrong.
const TRUE_MEANS: Record<string, 'good' | 'busy' | 'bad'> = {
  Available: 'good',
  Progressing: 'busy',
  Degraded: 'bad',
};

function tone(type: string, status: string): 'good' | 'busy' | 'bad' | 'idle' {
  if (status !== 'True') return 'idle';
  return TRUE_MEANS[type] ?? 'idle';
}

export function ClusterSummary({ cluster }: Props) {
  const upgrading = cluster.currentVersion !== '' && cluster.currentVersion !== cluster.version;

  return (
    <section aria-labelledby="cluster-heading" className="summary">
      <h2 id="cluster-heading">
        RedisCluster <code>{cluster.name}</code>
      </h2>
      <dl className="summary-facts">
        <div>
          <dt>Ready</dt>
          <dd>
            {cluster.readyReplicas} / {cluster.replicas}
          </dd>
        </div>
        <div>
          <dt>Desired version</dt>
          <dd>{cluster.version}</dd>
        </div>
        <div>
          <dt>Running version</dt>
          <dd>
            {cluster.currentVersion || '-'}
            {upgrading && <span className="muted"> (upgrading)</span>}
          </dd>
        </div>
      </dl>
      <ul className="conditions" aria-label="Conditions">
        {cluster.conditions.map((c) => (
          <li key={c.type} className="condition" data-tone={tone(c.type, c.status)} title={c.message || c.reason}>
            {c.type}: {c.status}
          </li>
        ))}
      </ul>
    </section>
  );
}
