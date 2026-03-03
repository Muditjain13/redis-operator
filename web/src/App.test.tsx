import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { FakeEventSource } from './test/fakeEventSource';
import { makeCluster, makeEvent, makeHeal, makePod, makeState } from './test/fixtures';
import type { PortalState } from './types';

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  FakeEventSource.reset();
  vi.stubGlobal('EventSource', FakeEventSource);
  fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
  delete document.documentElement.dataset.theme;
});

function showState(state: PortalState) {
  act(() => {
    FakeEventSource.latest.open();
    FakeEventSource.latest.emit('state', state);
  });
}

function reply(status: number, body: unknown) {
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(body), { status }));
}

describe('App', () => {
  it('says it is connecting until the first snapshot arrives', () => {
    render(<App />);
    expect(screen.getByText('Connecting to the cluster…')).toBeInTheDocument();
  });

  it('explains when the RedisCluster does not exist', () => {
    render(<App />);
    showState(makeState({ cluster: null, pods: [] }));
    expect(screen.getByText(/The RedisCluster is not deployed right now/)).toBeInTheDocument();
  });

  it('shows the cluster, its Pods, events and timings', () => {
    render(<App />);
    showState(
      makeState({
        events: [makeEvent({ reason: 'Healed', message: 'Recreated missing StatefulSet' })],
        heals: [makeHeal({ action: 'delete-statefulset', seconds: 39.2 })],
      }),
    );

    expect(screen.getByRole('heading', { name: /RedisCluster/ })).toBeInTheDocument();
    expect(screen.getByText('3 / 3')).toBeInTheDocument();
    expect(screen.getAllByRole('article')).toHaveLength(3);
    expect(screen.getByText('Recreated missing StatefulSet')).toBeInTheDocument();
    expect(screen.getByText('39.2 s')).toBeInTheDocument();
  });

  it('updates live as Pods change state', () => {
    render(<App />);
    showState(makeState());
    // Count the Pod state badges; "Ready" is also a label in the summary.
    expect(screen.getAllByText('Ready', { selector: '.badge' })).toHaveLength(3);

    act(() => {
      FakeEventSource.latest.emit(
        'state',
        makeState({
          cluster: makeCluster({ readyReplicas: 2 }),
          pods: [
            makePod({ uid: 'a', name: 'x-0' }),
            makePod({ uid: 'b', name: 'x-1', state: 'Terminating' }),
            makePod({ uid: 'c', name: 'x-2' }),
          ],
        }),
      );
    });

    expect(screen.getByRole('article', { name: 'x-1, Terminating' })).toBeInTheDocument();
    expect(screen.getByText('2 / 3')).toBeInTheDocument();
  });

  it('shows missing Pods as empty slots', () => {
    render(<App />);
    showState(makeState({ pods: [makePod({ name: 'x-0' })] }));
    expect(screen.getAllByText('Waiting for a Pod…')).toHaveLength(2);
  });

  it('runs an action, then locks the buttons for the cooldown', async () => {
    reply(202, { action: 'kill-pod', startedAt: '2026-01-01T12:00:00Z' });
    render(<App />);
    showState(makeState());

    await userEvent.click(screen.getByRole('button', { name: /Kill a random Pod/ }));

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/actions/kill-pod');
    expect(init.method).toBe('POST');
    expect(init.headers).toEqual({ 'X-Requested-With': 'redis-portal' });

    await waitFor(() => expect(screen.getByText(/You can run the next action in 30s/)).toBeInTheDocument());
    for (const button of screen.getAllByRole('button', { name: /Kill|Delete|Scale|Upgrade/ })) {
      expect(button).toBeDisabled();
    }
  });

  it('follows the server when it says to wait', async () => {
    reply(429, { error: 'please wait 20 seconds before the next action', retryAfterSeconds: 20 });
    render(<App />);
    showState(makeState());

    await userEvent.click(screen.getByRole('button', { name: /Scale up to 5/ }));

    await waitFor(() => expect(screen.getByText(/next action in 20s/)).toBeInTheDocument());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows the server message when an action is refused', async () => {
    reply(409, { error: 'another action is still in progress' });
    render(<App />);
    showState(makeState());

    await userEvent.click(screen.getByRole('button', { name: /Upgrade to Redis 7.4/ }));

    expect(await screen.findByRole('alert')).toHaveTextContent('another action is still in progress');
    // A refused action does not start a cooldown.
    expect(screen.getByRole('button', { name: /Upgrade to Redis 7.4/ })).toBeEnabled();
  });

  it('shows a friendly message when the portal cannot be reached', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
    render(<App />);
    showState(makeState());

    await userEvent.click(screen.getByRole('button', { name: /Delete the StatefulSet/ }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not reach the portal');
  });

  it('locks the buttons while the cluster is still settling after an action', () => {
    render(<App />);
    showState(makeState({ inFlight: { action: 'scale-up', startedAt: '2026-01-01T12:00:00Z' } }));
    for (const button of screen.getAllByRole('button', { name: /Kill|Delete|Scale|Upgrade/ })) {
      expect(button).toBeDisabled();
    }
    expect(screen.getByText(/Waiting for “Scale up to 5” to settle/)).toBeInTheDocument();
  });

  it('toggles between light and dark', async () => {
    render(<App />);
    showState(makeState());
    expect(document.documentElement.dataset.theme).toBe('light');

    await userEvent.click(screen.getByRole('button', { name: 'Light mode' }));

    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(screen.getByRole('button', { name: 'Dark mode' })).toBeInTheDocument();
  });

  it('shows the connection dropping and coming back', () => {
    render(<App />);
    showState(makeState());
    expect(screen.getByText('Live')).toBeInTheDocument();

    act(() => FakeEventSource.latest.fail());
    expect(screen.getByText('Reconnecting…')).toBeInTheDocument();

    act(() => FakeEventSource.latest.emit('state', makeState()));
    expect(screen.getByText('Live')).toBeInTheDocument();
  });
});
