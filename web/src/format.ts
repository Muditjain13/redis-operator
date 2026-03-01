/** Formats a duration measured by the backend, e.g. "39.2 s". */
export function formatSeconds(seconds: number): string {
  return `${seconds.toFixed(1)} s`;
}

/** Short relative age such as "12s", "3m" or "2h". */
export function formatAge(from: string, now: number): string {
  const started = Date.parse(from);
  if (Number.isNaN(started)) return '-';
  const seconds = Math.max(0, Math.floor((now - started) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

/** Wall-clock time of day, 24 hour, in the viewer's time zone. */
export function formatClock(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '--:--:--';
  return date.toLocaleTimeString('en-GB', { hour12: false });
}
