import { useState } from 'react';
import { ApiError, performAction } from './api';
import { COOLDOWN_SECONDS } from './actions';
import { ActionPanel } from './components/ActionPanel';
import { ClusterSummary } from './components/ClusterSummary';
import { EventFeed } from './components/EventFeed';
import { Header } from './components/Header';
import { HealTimings } from './components/HealTimings';
import { PodGrid } from './components/PodGrid';
import { useNow, useStream, useTheme } from './hooks';
import type { ActionName } from './types';

function describeError(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return 'Could not reach the portal. Please try again.';
}

export default function App() {
  const { state, status } = useStream();
  const { theme, toggle } = useTheme();
  const now = useNow();

  const [cooldownUntil, setCooldownUntil] = useState<number | null>(null);
  const [submitting, setSubmitting] = useState<ActionName | null>(null);
  const [error, setError] = useState<string | null>(null);

  const cooldown = cooldownUntil === null ? 0 : Math.max(0, Math.ceil((cooldownUntil - now) / 1000));

  const handleAction = async (action: ActionName) => {
    setSubmitting(action);
    setError(null);
    try {
      await performAction(action);
      setCooldownUntil(Date.now() + COOLDOWN_SECONDS * 1000);
    } catch (e) {
      if (e instanceof ApiError && e.status === 429 && e.retryAfterSeconds) {
        // The server is the authority on the cooldown: follow its number.
        setCooldownUntil(Date.now() + e.retryAfterSeconds * 1000);
      } else {
        setError(describeError(e));
      }
    } finally {
      setSubmitting(null);
    }
  };

  return (
    <div className="page">
      <Header status={status} theme={theme} onToggleTheme={toggle} />
      <main>
        {state === null ? (
          <p className="muted" role="status">
            Connecting to the cluster…
          </p>
        ) : state.cluster === null ? (
          <p role="status">The RedisCluster is not deployed right now. It will appear here when it is.</p>
        ) : (
          <>
            <ClusterSummary cluster={state.cluster} />
            <PodGrid pods={state.pods} desired={state.cluster.replicas} now={now} />
            <div className="two-column">
              <ActionPanel
                inFlight={state.inFlight}
                cooldown={cooldown}
                submitting={submitting}
                error={error}
                onAction={(a) => void handleAction(a)}
              />
              <HealTimings heals={state.heals} now={now} />
            </div>
            <EventFeed events={state.events} />
          </>
        )}
      </main>
    </div>
  );
}
