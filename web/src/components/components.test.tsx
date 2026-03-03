import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { makeCluster, makeEvent, makeHeal, makePod } from '../test/fixtures';
import { ActionPanel } from './ActionPanel';
import { ClusterSummary } from './ClusterSummary';
import { EventFeed } from './EventFeed';
import { Header } from './Header';
import { HealTimings } from './HealTimings';
import { PodCard } from './PodCard';
import { PodGrid } from './PodGrid';

const NOW = Date.parse('2026-01-01T12:01:00Z');

describe('PodCard', () => {
  it('shows the Pod name, state, image, node and age', () => {
    render(<PodCard pod={makePod({ name: 'x-0', state: 'Ready' })} now={NOW} />);

    const card = screen.getByRole('article', { name: 'x-0, Ready' });
    expect(within(card).getByRole('heading', { name: 'x-0' })).toBeInTheDocument();
    expect(within(card).getByText('Ready')).toBeInTheDocument();
    expect(within(card).getByText('redis:7.2')).toBeInTheDocument();
    expect(within(card).getByText('kind-control-plane')).toBeInTheDocument();
    expect(within(card).getByText('1m')).toBeInTheDocument();
  });

  it.each(['Pending', 'Running', 'Ready', 'Terminating', 'Failed'] as const)('marks the %s state for styling', (state) => {
    render(<PodCard pod={makePod({ state })} now={NOW} />);
    expect(screen.getByRole('article')).toHaveAttribute('data-state', state);
  });

  it('shows a dash for facts that are not known yet', () => {
    render(<PodCard pod={makePod({ node: undefined, image: '' })} now={NOW} />);
    expect(screen.getAllByText('-')).toHaveLength(2);
  });
});

describe('PodGrid', () => {
  it('renders one card per Pod', () => {
    const pods = [makePod({ uid: 'a', name: 'x-0' }), makePod({ uid: 'b', name: 'x-1' })];
    render(<PodGrid pods={pods} desired={2} now={NOW} />);
    expect(screen.getAllByRole('article')).toHaveLength(2);
    expect(screen.queryByText(/Waiting for a Pod/)).not.toBeInTheDocument();
  });

  it('shows an empty slot for every Pod that is missing', () => {
    render(<PodGrid pods={[makePod({ name: 'x-0' })]} desired={3} now={NOW} />);
    expect(screen.getAllByRole('article')).toHaveLength(1);
    expect(screen.getAllByText('Waiting for a Pod…')).toHaveLength(2);
  });

  it('says so when a cluster has no Pods', () => {
    render(<PodGrid pods={[]} desired={0} now={NOW} />);
    expect(screen.getByText('There are no Pods.')).toBeInTheDocument();
  });
});

describe('ClusterSummary', () => {
  it('shows readiness and versions', () => {
    render(<ClusterSummary cluster={makeCluster({ readyReplicas: 2, replicas: 3 })} />);
    expect(screen.getByText('2 / 3')).toBeInTheDocument();
    expect(screen.getAllByText('7.2')).toHaveLength(2);
    expect(screen.queryByText('(upgrading)')).not.toBeInTheDocument();
  });

  it('says when an upgrade is in progress', () => {
    render(<ClusterSummary cluster={makeCluster({ version: '7.4', currentVersion: '7.2' })} />);
    expect(screen.getByText('(upgrading)')).toBeInTheDocument();
  });

  it('does not call a first rollout an upgrade', () => {
    render(<ClusterSummary cluster={makeCluster({ currentVersion: '' })} />);
    expect(screen.queryByText('(upgrading)')).not.toBeInTheDocument();
  });

  it('colours each condition by what it means', () => {
    const conditions = [
      { type: 'Available', status: 'True' },
      { type: 'Progressing', status: 'True' },
      { type: 'Degraded', status: 'True' },
      { type: 'Unknown thing', status: 'True' },
      { type: 'Degraded', status: 'False' },
    ];
    render(<ClusterSummary cluster={makeCluster({ conditions })} />);

    const items = within(screen.getByRole('list', { name: 'Conditions' })).getAllByRole('listitem');
    expect(items.map((li) => li.getAttribute('data-tone'))).toEqual(['good', 'busy', 'bad', 'idle', 'idle']);
  });
});

describe('ActionPanel', () => {
  const noop = () => {};

  it('offers every action', () => {
    render(<ActionPanel inFlight={null} cooldown={0} submitting={null} error={null} onAction={noop} />);
    for (const name of [
      'Kill a random Pod',
      'Delete the StatefulSet',
      'Scale up to 5',
      'Scale back to 3',
      'Upgrade to Redis 7.4',
    ]) {
      expect(screen.getByRole('button', { name: new RegExp(name) })).toBeEnabled();
    }
  });

  it('says who repairs the cluster after each action', () => {
    render(<ActionPanel inFlight={null} cooldown={0} submitting={null} error={null} onAction={noop} />);
    const kill = screen.getByRole('button', { name: /Kill a random Pod/ });
    expect(kill).toHaveAccessibleDescription(/Healed by Kubernetes/);
    const del = screen.getByRole('button', { name: /Delete the StatefulSet/ });
    expect(del).toHaveAccessibleDescription(/Healed by the operator/);
  });

  it('reports the clicked action', async () => {
    const onAction = vi.fn();
    render(<ActionPanel inFlight={null} cooldown={0} submitting={null} error={null} onAction={onAction} />);

    await userEvent.click(screen.getByRole('button', { name: /Scale up to 5/ }));

    expect(onAction).toHaveBeenCalledExactlyOnceWith('scale-up');
  });

  it('locks every button while an action is settling and says which', () => {
    render(
      <ActionPanel
        inFlight={{ action: 'upgrade', startedAt: '2026-01-01T12:00:00Z' }}
        cooldown={0}
        submitting={null}
        error={null}
        onAction={noop}
      />,
    );
    for (const button of screen.getAllByRole('button')) expect(button).toBeDisabled();
    expect(screen.getByRole('status')).toHaveTextContent('Waiting for “Upgrade to Redis 7.4” to settle…');
  });

  it('locks every button during the cooldown and shows the countdown', () => {
    render(<ActionPanel inFlight={null} cooldown={23} submitting={null} error={null} onAction={noop} />);
    for (const button of screen.getAllByRole('button')) expect(button).toBeDisabled();
    expect(screen.getByRole('status')).toHaveTextContent('You can run the next action in 23s.');
  });

  it('locks every button while a request is being sent', () => {
    render(<ActionPanel inFlight={null} cooldown={0} submitting="kill-pod" error={null} onAction={noop} />);
    for (const button of screen.getAllByRole('button')) expect(button).toBeDisabled();
  });

  it('announces an error', () => {
    render(<ActionPanel inFlight={null} cooldown={0} submitting={null} error="the action failed" onAction={noop} />);
    expect(screen.getByRole('alert')).toHaveTextContent('the action failed');
  });
});

describe('HealTimings', () => {
  it('invites the visitor to run an action when there is nothing yet', () => {
    render(<HealTimings heals={[]} now={NOW} />);
    expect(screen.getByText(/Run an action to see how long/)).toBeInTheDocument();
  });

  it('lists results with the measured time', () => {
    const heals = [makeHeal({ action: 'kill-pod', seconds: 12.34 }), makeHeal({ action: 'upgrade', seconds: 52 })];
    render(<HealTimings heals={heals} now={NOW} />);

    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(2);
    expect(within(items[0]!).getByText('Kill a random Pod')).toBeInTheDocument();
    expect(within(items[0]!).getByText('12.3 s')).toBeInTheDocument();
    expect(within(items[1]!).getByText('52.0 s')).toBeInTheDocument();
  });

  it('flags a timeout instead of presenting it as a heal time', () => {
    render(<HealTimings heals={[makeHeal({ timedOut: true, seconds: 300 })]} now={NOW} />);
    expect(screen.getByText('timed out')).toBeInTheDocument();
    expect(screen.getByText(/after 300\.0 s/)).toBeInTheDocument();
  });

  it('shows only the most recent results', () => {
    const heals = Array.from({ length: 12 }, (_, i) => makeHeal({ finishedAt: `2026-01-01T12:00:${String(i).padStart(2, '0')}Z` }));
    render(<HealTimings heals={heals} now={NOW} />);
    expect(screen.getAllByRole('listitem')).toHaveLength(8);
  });
});

describe('EventFeed', () => {
  it('says so when there are no events', () => {
    render(<EventFeed events={[]} />);
    expect(screen.getByText('No events yet.')).toBeInTheDocument();
  });

  it('shows the newest event first', () => {
    const events = [
      makeEvent({ time: '2026-01-01T12:00:01Z', reason: 'Created', message: 'Created StatefulSet' }),
      makeEvent({ time: '2026-01-01T12:00:09Z', reason: 'Healed', message: 'Recreated missing StatefulSet' }),
    ];
    render(<EventFeed events={events} />);

    const items = within(screen.getByRole('log')).getAllByRole('listitem');
    expect(items).toHaveLength(2);
    expect(within(items[0]!).getByText('Healed')).toBeInTheDocument();
    expect(within(items[1]!).getByText('Created')).toBeInTheDocument();
  });

  it('does not reorder the list it was given', () => {
    const events = [makeEvent({ reason: 'A', message: 'a' }), makeEvent({ reason: 'B', message: 'b' })];
    render(<EventFeed events={events} />);
    expect(events.map((e) => e.reason)).toEqual(['A', 'B']);
  });

  it('keeps the log short', () => {
    const events = Array.from({ length: 30 }, (_, i) => makeEvent({ message: `event ${i}` }));
    render(<EventFeed events={events} />);
    expect(screen.getAllByRole('listitem')).toHaveLength(20);
  });
});

describe('Header', () => {
  it.each([
    ['connecting', 'Connecting…'],
    ['live', 'Live'],
    ['reconnecting', 'Reconnecting…'],
  ] as const)('shows the %s connection status', (status, label) => {
    render(<Header status={status} theme="light" onToggleTheme={() => {}} />);
    expect(screen.getByRole('status')).toHaveTextContent(label);
  });

  it('toggles the theme and reports which one is on', async () => {
    const onToggle = vi.fn();
    const { rerender } = render(<Header status="live" theme="light" onToggleTheme={onToggle} />);

    const button = screen.getByRole('button', { name: 'Light mode' });
    expect(button).toHaveAttribute('aria-pressed', 'false');
    await userEvent.click(button);
    expect(onToggle).toHaveBeenCalledTimes(1);

    rerender(<Header status="live" theme="dark" onToggleTheme={onToggle} />);
    expect(screen.getByRole('button', { name: 'Dark mode' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('links to the source safely', () => {
    render(<Header status="live" theme="light" onToggleTheme={() => {}} />);
    const link = screen.getByRole('link', { name: 'Source' });
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(link).toHaveAttribute('target', '_blank');
  });
});
