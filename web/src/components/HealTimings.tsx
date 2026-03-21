import { actionLabel } from '../actions';
import { formatAge, formatSeconds } from '../format';
import type { HealRecord } from '../types';

interface Props {
  heals: HealRecord[];
  now: number;
}

const SHOWN = 8;

export function HealTimings({ heals, now }: Props) {
  return (
    <section aria-labelledby="heals-heading">
      <h2 id="heals-heading">Recovery times</h2>
      {heals.length === 0 ? (
        <p className="muted">Run an action to see how long the cluster takes to settle.</p>
      ) : (
        <ol className="heals">
          {heals.slice(0, SHOWN).map((h) => (
            <li key={`${h.action}-${h.finishedAt}`} className="heal">
              <span className="heal-action">{actionLabel(h.action)}</span>
              <span className="heal-seconds">
                {h.timedOut ? (
                  <>
                    <span className="tag-bad">timed out</span> after {formatSeconds(h.seconds)}
                  </>
                ) : (
                  formatSeconds(h.seconds)
                )}
              </span>
              <span className="heal-when muted">{formatAge(h.finishedAt, now)} ago</span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
