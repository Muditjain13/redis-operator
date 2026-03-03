import { formatClock } from '../format';
import type { ClusterEvent } from '../types';

interface Props {
  events: ClusterEvent[];
}

const SHOWN = 20;

export function EventFeed({ events }: Props) {
  // The backend sends oldest first; a feed reads better newest first.
  const newestFirst = [...events].reverse().slice(0, SHOWN);

  return (
    <section aria-labelledby="events-heading">
      <h2 id="events-heading">Operator events</h2>
      {newestFirst.length === 0 ? (
        <p className="muted">No events yet.</p>
      ) : (
        <ul className="events" role="log" aria-live="polite">
          {newestFirst.map((e) => (
            <li key={`${e.time}-${e.reason}-${e.message}`} className="event" data-type={e.type}>
              <time dateTime={e.time}>{formatClock(e.time)}</time>
              <span className="event-reason">{e.reason}</span>
              <span className="event-message">{e.message}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
