import type { ActionName, Operation } from './types';

/** Sent with every action. The backend rejects POSTs without it, which blocks cross-site forgery. */
export const REQUESTED_WITH = 'redis-portal';

export class ApiError extends Error {
  readonly status: number;
  readonly retryAfterSeconds: number | undefined;

  constructor(message: string, status: number, retryAfterSeconds?: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

interface ErrorBody {
  error?: string;
  retryAfterSeconds?: number;
}

/** Runs one of the fixed demo actions. Resolves once the backend has accepted it. */
export async function performAction(action: ActionName, fetchImpl: typeof fetch = fetch): Promise<Operation> {
  const response = await fetchImpl(`/api/actions/${action}`, {
    method: 'POST',
    headers: { 'X-Requested-With': REQUESTED_WITH },
  });

  if (response.ok) {
    return (await response.json()) as Operation;
  }

  let body: ErrorBody = {};
  try {
    body = (await response.json()) as ErrorBody;
  } catch {
    // The body was not JSON, e.g. a proxy error page. Fall back to the status.
  }
  throw new ApiError(body.error ?? `Request failed (${response.status})`, response.status, body.retryAfterSeconds);
}
