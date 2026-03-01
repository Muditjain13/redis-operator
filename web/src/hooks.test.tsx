import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { normalizeState, useNow, useStream, useTheme } from './hooks';
import { FakeEventSource, createFakeSource } from './test/fakeEventSource';
import { makeState } from './test/fixtures';

beforeEach(() => {
  FakeEventSource.reset();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  localStorage.clear();
  delete document.documentElement.dataset.theme;
});

describe('normalizeState', () => {
  it('turns missing lists into empty ones so components never see null', () => {
    expect(normalizeState({})).toEqual({ cluster: null, pods: [], events: [], heals: [], inFlight: null });
  });

  it('keeps what the backend sent', () => {
    const state = makeState();
    expect(normalizeState(state)).toEqual(state);
  });
});

describe('useStream', () => {
  it('starts connecting with no state', () => {
    const { result } = renderHook(() => useStream('/api/stream', createFakeSource));
    expect(result.current).toEqual({ state: null, status: 'connecting' });
    expect(FakeEventSource.latest.url).toBe('/api/stream');
  });

  it('goes live and shows each state snapshot it receives', () => {
    const { result } = renderHook(() => useStream('/api/stream', createFakeSource));
    const source = FakeEventSource.latest;

    act(() => source.open());
    expect(result.current.status).toBe('live');

    const first = makeState();
    act(() => source.emit('state', first));
    expect(result.current.state).toEqual(first);

    const second = makeState({ inFlight: { action: 'kill-pod', startedAt: '2026-01-01T12:00:00Z' } });
    act(() => source.emit('state', second));
    expect(result.current.state?.inFlight?.action).toBe('kill-pod');
  });

  it('fills in lists the backend left null', () => {
    const { result } = renderHook(() => useStream('/api/stream', createFakeSource));
    act(() => FakeEventSource.latest.emit('state', '{"cluster":null,"pods":null,"events":null,"heals":null}'));
    expect(result.current.state).toEqual({ cluster: null, pods: [], events: [], heals: [], inFlight: null });
  });

  it('reports reconnecting after an error and recovers when a message arrives', () => {
    const { result } = renderHook(() => useStream('/api/stream', createFakeSource));
    const source = FakeEventSource.latest;
    act(() => source.open());

    act(() => source.fail());
    expect(result.current.status).toBe('reconnecting');

    act(() => source.emit('state', makeState()));
    expect(result.current.status).toBe('live');
  });

  it('ignores a malformed message and keeps the last good state', () => {
    const { result } = renderHook(() => useStream('/api/stream', createFakeSource));
    const source = FakeEventSource.latest;
    const good = makeState();
    act(() => source.emit('state', good));

    act(() => source.emit('state', '{not json'));

    expect(result.current.state).toEqual(good);
  });

  it('closes the connection when the component goes away', () => {
    const { unmount } = renderHook(() => useStream('/api/stream', createFakeSource));
    const source = FakeEventSource.latest;
    expect(source.closed).toBe(false);
    unmount();
    expect(source.closed).toBe(true);
  });

  it('opens one connection, not one per render', () => {
    const { rerender } = renderHook(() => useStream('/api/stream', createFakeSource));
    rerender();
    rerender();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('uses the browser EventSource by default', () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    renderHook(() => useStream());
    expect(FakeEventSource.latest.url).toBe('/api/stream');
  });
});

describe('useNow', () => {
  it('ticks with the interval and stops on unmount', () => {
    vi.useFakeTimers({ now: new Date('2026-01-01T12:00:00Z') });
    const { result, unmount } = renderHook(() => useNow(1000));
    const start = result.current;

    act(() => vi.advanceTimersByTime(3000));
    expect(result.current - start).toBe(3000);

    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('useTheme', () => {
  it('uses a saved choice over the system setting', () => {
    localStorage.setItem('theme', 'dark');
    const { result } = renderHook(() => useTheme());
    expect(result.current.theme).toBe('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
  });

  it('follows the system setting when nothing is saved', () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true }));
    const { result } = renderHook(() => useTheme());
    expect(result.current.theme).toBe('dark');
  });

  it('defaults to light', () => {
    const { result } = renderHook(() => useTheme());
    expect(result.current.theme).toBe('light');
  });

  it('ignores a junk saved value', () => {
    localStorage.setItem('theme', 'purple');
    const { result } = renderHook(() => useTheme());
    expect(result.current.theme).toBe('light');
  });

  it('toggles, applies the theme to the page and remembers it', () => {
    const { result } = renderHook(() => useTheme());

    act(() => result.current.toggle());
    expect(result.current.theme).toBe('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(localStorage.getItem('theme')).toBe('dark');

    act(() => result.current.toggle());
    expect(result.current.theme).toBe('light');
    expect(localStorage.getItem('theme')).toBe('light');
  });

  it('still works when storage is blocked', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });

    const { result } = renderHook(() => useTheme());
    expect(result.current.theme).toBe('light');
    act(() => result.current.toggle());
    expect(result.current.theme).toBe('dark');

    vi.restoreAllMocks();
  });
});
