import type { Theme } from '../hooks';
import type { ConnectionStatus } from '../types';

interface Props {
  status: ConnectionStatus;
  theme: Theme;
  onToggleTheme: () => void;
}

const STATUS_LABEL: Record<ConnectionStatus, string> = {
  connecting: 'Connecting…',
  live: 'Live',
  reconnecting: 'Reconnecting…',
};

export function Header({ status, theme, onToggleTheme }: Props) {
  return (
    <header className="top">
      <div>
        <h1>Redis operator</h1>
        <p className="muted">
          A live RedisCluster in the demo namespace. Delete or change things and see how long it takes to recover.
        </p>
      </div>
      <div className="top-controls">
        <span className="live" data-status={status} role="status">
          <span className="dot" aria-hidden="true" />
          {STATUS_LABEL[status]}
        </span>
        <button type="button" className="theme-toggle" onClick={onToggleTheme} aria-pressed={theme === 'dark'}>
          {theme === 'dark' ? 'Dark' : 'Light'} mode
        </button>
        <a href="https://github.com/Muditjain13/redis-operator" rel="noopener noreferrer" target="_blank">
          Source
        </a>
      </div>
    </header>
  );
}
