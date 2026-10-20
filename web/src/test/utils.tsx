import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ReactElement } from 'react';
import { MemoryRouter } from 'react-router';
import { vi } from 'vitest';

/** Renders ui with a fresh query client (no retries) and an in-memory router. */
export function renderWithProviders(ui: ReactElement, { route = '/' }: { route?: string } = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, retryDelay: 0 }, mutations: { retry: false } },
  });
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>
      </QueryClientProvider>,
    ),
  };
}

type Reply = { status?: number; body?: unknown; headers?: Record<string, string> };
type Handler = (url: string, init: RequestInit) => Reply | Promise<Reply>;

/**
 * Replaces fetch with a fake that answers "METHOD /api/path" keys from
 * routes, and records every call. Unknown routes answer 404.
 */
export function mockApi(routes: Record<string, Reply | Handler>) {
  const calls: { method: string; url: string; body: unknown }[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input);
    const method = (init.method ?? 'GET').toUpperCase();
    const body = typeof init.body === 'string' ? JSON.parse(init.body) : init.body;
    calls.push({ method, url, body });
    const path = url.split('?')[0];
    const route = routes[`${method} ${url}`] ?? routes[`${method} ${path}`];
    const reply: Reply = typeof route === 'function' ? await route(url, init) : (route ?? { status: 404, body: { error: { code: 'NOT_FOUND', message: 'not found' } } });
    const status = reply.status ?? 200;
    return new Response(status === 204 || reply.body === undefined ? null : JSON.stringify(reply.body), {
      status,
      headers: { 'Content-Type': 'application/json', ...reply.headers },
    });
  });
  vi.stubGlobal('fetch', fetchMock);
  return { calls, fetchMock };
}

export const testUser = { id: 1, email: 'asha@example.com', name: 'Asha', role: 'user' as const, workspace_id: 1 };
