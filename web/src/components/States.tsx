import type { ReactNode } from 'react';
import { ApiError } from '../api/client';

export function Loading({ label = 'Loading' }: { label?: string }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" />
      <span>{label}…</span>
    </div>
  );
}

/** Turns an API error into a sentence a user can act on. */
export function describeError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return error.message;
    if (error.status === 429) {
      return error.retryAfter
        ? `Too many attempts. Try again in ${error.retryAfter} seconds.`
        : 'Too many attempts. Try again in a moment.';
    }
    if (error.status >= 500) return 'The server ran into a problem. Try again in a moment.';
    return error.message;
  }
  return error instanceof Error ? error.message : 'Something went wrong.';
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div className="state state-error" role="alert">
      <p>{describeError(error)}</p>
      {onRetry && (
        <button type="button" className="btn btn-secondary" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="state state-empty">
      <p className="state-title">{title}</p>
      {children}
    </div>
  );
}
