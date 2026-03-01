import { describe, expect, it } from 'vitest';
import { formatAge, formatClock, formatSeconds } from './format';

describe('formatSeconds', () => {
  it('shows one decimal place and the unit', () => {
    expect(formatSeconds(39.2061744)).toBe('39.2 s');
    expect(formatSeconds(0)).toBe('0.0 s');
    expect(formatSeconds(125)).toBe('125.0 s');
  });
});

describe('formatAge', () => {
  const from = '2026-01-01T12:00:00Z';
  const at = (seconds: number) => Date.parse(from) + seconds * 1000;

  it.each([
    [0, '0s'],
    [59, '59s'],
    [60, '1m'],
    [3599, '59m'],
    [3600, '1h'],
    [47 * 3600, '47h'],
    [48 * 3600, '2d'],
  ])('formats %i seconds as %s', (seconds, expected) => {
    expect(formatAge(from, at(seconds))).toBe(expected);
  });

  it('never goes negative when the clocks disagree', () => {
    expect(formatAge(from, at(-30))).toBe('0s');
  });

  it('shows a dash for an unparseable time', () => {
    expect(formatAge('not a date', Date.now())).toBe('-');
  });
});

describe('formatClock', () => {
  it('formats a time of day with seconds', () => {
    expect(formatClock('2026-01-01T12:34:56Z')).toMatch(/^\d{2}:\d{2}:\d{2}$/);
  });

  it('shows placeholders for an unparseable time', () => {
    expect(formatClock('nope')).toBe('--:--:--');
  });
});
