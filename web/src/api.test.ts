import { describe, expect, it, vi } from 'vitest';
import { ApiError, REQUESTED_WITH, performAction } from './api';

function respond(status: number, body: unknown, asText = false): Response {
  return new Response(asText ? String(body) : JSON.stringify(body), { status });
}

describe('performAction', () => {
  it('POSTs to the action URL with the anti-forgery header and returns the operation', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(202, { action: 'kill-pod', startedAt: '2026-01-01T12:00:00Z' }));

    const op = await performAction('kill-pod', fetchMock);

    expect(op.action).toBe('kill-pod');
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/actions/kill-pod');
    expect(init.method).toBe('POST');
    expect(init.headers).toEqual({ 'X-Requested-With': REQUESTED_WITH });
    expect(init.body).toBeUndefined(); // actions take no input
  });

  it('throws an ApiError carrying the server message and the wait time on a 429', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(respond(429, { error: 'please wait 20 seconds before the next action', retryAfterSeconds: 20 }));

    const error = await performAction('scale-up', fetchMock).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 429, retryAfterSeconds: 20, message: 'please wait 20 seconds before the next action' });
  });

  it('reports a conflict with its own message', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(409, { error: 'another action is still in progress' }));
    await expect(performAction('upgrade', fetchMock)).rejects.toMatchObject({
      status: 409,
      message: 'another action is still in progress',
    });
  });

  it('falls back to the status when the error body is not JSON', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(502, '<html>Bad gateway</html>', true));
    await expect(performAction('kill-pod', fetchMock)).rejects.toMatchObject({
      status: 502,
      message: 'Request failed (502)',
    });
  });

  it('lets a network failure propagate', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
    await expect(performAction('kill-pod', fetchMock)).rejects.toThrow('Failed to fetch');
  });
});
