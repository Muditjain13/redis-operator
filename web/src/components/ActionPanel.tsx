import { ACTIONS, actionLabel } from '../actions';
import type { ActionName, Operation } from '../types';

interface Props {
  /** The action that is still settling, if any. Only one can run at a time. */
  inFlight: Operation | null;
  /** Whole seconds until this visitor may act again. */
  cooldown: number;
  /** The action whose request is being sent right now. */
  submitting: ActionName | null;
  /** The reason the last request failed, if it did. */
  error: string | null;
  onAction: (action: ActionName) => void;
}

export function ActionPanel({ inFlight, cooldown, submitting, error, onAction }: Props) {
  const locked = inFlight !== null || cooldown > 0 || submitting !== null;

  let status: string | null = null;
  if (inFlight) status = `Waiting for “${actionLabel(inFlight.action)}” to settle…`;
  else if (cooldown > 0) status = `You can run the next action in ${cooldown}s.`;

  return (
    <section aria-labelledby="actions-heading">
      <h2 id="actions-heading">Actions</h2>
      <div className="actions">
        {ACTIONS.map((a) => (
          <button
            key={a.name}
            type="button"
            className="action"
            disabled={locked}
            onClick={() => onAction(a.name)}
            aria-describedby={`hint-${a.name}`}
          >
            <span className="action-label">{a.label}</span>
            <span className="action-hint" id={`hint-${a.name}`}>
              {a.hint} Healed by {a.healedBy}.
            </span>
          </button>
        ))}
      </div>
      <p className="action-status" role="status">
        {status}
      </p>
      {error && (
        <p className="action-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
