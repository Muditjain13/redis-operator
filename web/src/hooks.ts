import { useEffect, useState } from 'react';
import type { ConnectionStatus, PortalState } from './types';

/** Re-renders every `intervalMs` and returns the current time, so ages and countdowns tick. */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

/** Fills in anything the backend left out, so components never see null lists. */
export function normalizeState(raw: Partial<PortalState>): PortalState {
  return {
    cluster: raw.cluster ?? null,
    pods: raw.pods ?? [],
    events: raw.events ?? [],
    heals: raw.heals ?? [],
    inFlight: raw.inFlight ?? null,
  };
}

// Module level on purpose: a default argument written inline would be a new
// function on every render, change the effect's dependencies, and reconnect forever.
const defaultCreateSource = (url: string): EventSource => new EventSource(url);

/**
 * Follows the portal's Server-Sent Events stream. Every "state" message is a
 * full snapshot, so the UI never has to merge partial updates. The browser
 * reconnects by itself after a drop; this only reports the connection status.
 */
export function useStream(
  url = '/api/stream',
  createSource: (url: string) => EventSource = defaultCreateSource,
): { state: PortalState | null; status: ConnectionStatus } {
  const [state, setState] = useState<PortalState | null>(null);
  const [status, setStatus] = useState<ConnectionStatus>('connecting');

  useEffect(() => {
    const source = createSource(url);
    source.onopen = () => setStatus('live');
    source.onerror = () => setStatus('reconnecting');
    source.addEventListener('state', (event) => {
      try {
        setState(normalizeState(JSON.parse((event as MessageEvent<string>).data) as Partial<PortalState>));
        setStatus('live');
      } catch {
        // Ignore a malformed message; the next snapshot replaces it anyway.
      }
    });
    return () => source.close();
  }, [url, createSource]);

  return { state, status };
}

export type Theme = 'light' | 'dark';

function initialTheme(): Theme {
  try {
    const saved = localStorage.getItem('theme');
    if (saved === 'light' || saved === 'dark') return saved;
  } catch {
    // Storage can be blocked (private mode); fall through to the system setting.
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

/** Light or dark mode: the saved choice, else the system setting. Applied as data-theme on <html>. */
export function useTheme(): { theme: Theme; toggle: () => void } {
  const [theme, setTheme] = useState<Theme>(initialTheme);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  const toggle = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark';
    setTheme(next);
    try {
      localStorage.setItem('theme', next);
    } catch {
      // Not persisting is fine.
    }
  };

  return { theme, toggle };
}
