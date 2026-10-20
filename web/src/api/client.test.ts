import { afterEach, describe, expect, it, vi } from 'vitest';
import { mockApi } from '../test/utils';
import { ApiError, api, queryString } from './client';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('queryString', () => {
  it('leaves out empty values', () => {
    expect(queryString({ q: 'sale', status: undefined, campaign_id: 3, cursor: '', tag_id: null })).toBe('?q=sale&campaign_id=3');
    expect(queryString({})).toBe('');
  });
});

describe('api client', () => {
  it('sends JSON with the session cookie and returns the body', async () => {
    const { fetchMock, calls } = mockApi({ 'POST /api/auth/login': { body: { user: { id: 1, email: 'a@b.co' } } } });
    const res = await api.auth.login({ email: 'a@b.co', password: 'secret-pass' });
    expect(res.user.id).toBe(1);
    expect(calls[0].body).toEqual({ email: 'a@b.co', password: 'secret-pass' });
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.credentials).toBe('same-origin');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
  });

  it('turns the error envelope into an ApiError', async () => {
    mockApi({ 'GET /api/links/9': { status: 404, body: { error: { code: 'NOT_FOUND', message: 'link not found' } } } });
    const err = await api.links.get(9).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 404, code: 'NOT_FOUND', message: 'link not found' });
  });

  it('keeps field errors and warnings from a 422', async () => {
    const body = {
      errors: [{ field: 'alias', code: 'ALIAS_TAKEN', message: 'alias is already taken' }],
      warnings: [{ field: 'destination', code: 'HTTP_NOT_HTTPS', message: 'destination uses http, not https' }],
    };
    mockApi({ 'POST /api/links': { status: 422, body } });
    const err = (await api.links.create({ destination: 'http://a.example', alias: 'Sale' }).catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(422);
    expect(err.code).toBe('ALIAS_TAKEN');
    expect(err.validation).toEqual(body);
  });

  it('reads Retry-After on 429', async () => {
    mockApi({ 'POST /api/auth/login': { status: 429, body: { error: { code: 'RATE_LIMITED', message: 'slow down' } }, headers: { 'Retry-After': '42' } } });
    const err = (await api.auth.login({ email: 'a@b.co', password: 'x' }).catch((e: unknown) => e)) as ApiError;
    expect(err.retryAfter).toBe(42);
  });

  it('handles 204 and network failures', async () => {
    mockApi({ 'DELETE /api/links/3': { status: 204 } });
    await expect(api.links.remove(3)).resolves.toBeUndefined();

    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    const err = (await api.auth.me().catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(0);
    expect(err.code).toBe('NETWORK');
  });

  it('builds list and file URLs', async () => {
    const { calls } = mockApi({
      'GET /api/links': { body: { items: [], next_cursor: null } },
      'DELETE /api/admin/blocklist/a%2Fb.example': { status: 204 },
    });
    await api.links.list({ q: 'diwali', status: 'active', limit: 20 });
    expect(calls[0].url).toBe('/api/links?q=diwali&status=active&limit=20');
    expect(api.links.qrUrl(5, 'png', 256)).toBe('/api/links/5/qr?format=png&size=256');
    await api.admin.unblock('a/b.example');
    expect(calls[1].url).toBe('/api/admin/blocklist/a%2Fb.example');
  });
});
