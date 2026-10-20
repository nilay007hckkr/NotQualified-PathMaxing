import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from '../App';
import { sampleLink } from '../test/fixtures';
import { mockApi, renderWithProviders, testUser } from '../test/utils';

afterEach(() => {
  vi.unstubAllGlobals();
});

const admin = { body: { user: { ...testUser, role: 'admin' as const } } };
const empty = { body: { items: [], next_cursor: null } };

describe('admin area', () => {
  it('is hidden from and closed to regular users', async () => {
    mockApi({
      'GET /api/auth/me': { body: { user: testUser } },
      'GET /api/analytics/overview': { status: 500, body: { error: { code: 'INTERNAL', message: 'x' } } },
      'GET /api/alerts': { body: { items: [] } },
    });
    renderWithProviders(<App />, { route: '/admin/abuse' });
    expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument();
  });

  it('disables a quarantined link only with a reason', async () => {
    const { calls } = mockApi({
      'GET /api/auth/me': admin,
      'GET /api/admin/abuse': { body: { items: [{ ...sampleLink, trust_state: 'quarantined' }], next_cursor: null } },
      'POST /api/admin/links/42/disable': { body: { link: { ...sampleLink, status: 'disabled_admin' } } },
    });
    renderWithProviders(<App />, { route: '/admin/abuse' });
    expect(await screen.findByRole('link', { name: 'Admin' })).toBeInTheDocument();
    const row = (await screen.findByText('/diwali')).closest('tr') as HTMLElement;
    expect(within(row).getByText('Quarantined')).toBeInTheDocument();

    await userEvent.click(within(row).getByRole('button', { name: 'Disable /diwali' }));
    await userEvent.click(within(row).getByRole('button', { name: 'Disable link' }));
    expect(within(row).getByText('Give a reason; it goes into the audit log.')).toBeInTheDocument();
    expect(calls.some((c) => c.url.endsWith('/disable'))).toBe(false);

    await userEvent.type(within(row).getByLabelText('Reason for disabling /diwali'), 'phishing kit');
    await userEvent.click(within(row).getByRole('button', { name: 'Disable link' }));
    await waitFor(() => expect(calls.find((c) => c.url === '/api/admin/links/42/disable')?.body).toEqual({ reason: 'phishing kit' }));
  });

  it('blocks a domain (and says subdomains are included), then unblocks it after confirming', async () => {
    const { calls } = mockApi({
      'GET /api/auth/me': admin,
      'GET /api/admin/blocklist': { body: { items: [{ domain: 'evil.example', reason: 'phishing', created_at: '2026-10-20T08:00:00Z' }], next_cursor: null } },
      'POST /api/admin/blocklist': { status: 201, body: { blocked_domain: { domain: 'bad.example', reason: 'malware', created_at: '2026-10-20T09:00:00Z' } } },
      'DELETE /api/admin/blocklist/evil.example': { status: 204 },
    });
    renderWithProviders(<App />, { route: '/admin/blocklist' });
    expect(await screen.findByText(/also blocks all of its subdomains/)).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText('Domain'), 'https://bad.example/x');
    await userEvent.type(screen.getByLabelText('Reason'), 'malware');
    await userEvent.click(screen.getByRole('button', { name: 'Block domain' }));
    expect(screen.getByText(/Enter a domain name only/)).toBeInTheDocument();
    await userEvent.clear(screen.getByLabelText('Domain'));
    await userEvent.type(screen.getByLabelText('Domain'), 'Bad.Example');
    await userEvent.click(screen.getByRole('button', { name: 'Block domain' }));
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ domain: 'bad.example', reason: 'malware' }));

    await userEvent.click(await screen.findByRole('button', { name: 'Remove evil.example' }));
    await userEvent.click(screen.getByRole('button', { name: 'Unblock' }));
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/admin/blocklist/evil.example')).toBe(true));
  });

  it('pages through the audit log with the cursor', async () => {
    const entry = (id: number) => ({ id, actor_id: 1, action: 'link.disable', entity_type: 'link', entity_id: 42, detail: { reason: 'phishing kit' }, ts: '2026-10-20T08:00:00Z' });
    const { calls } = mockApi({
      'GET /api/auth/me': admin,
      'GET /api/admin/audit': (url) => ({ body: url.includes('cursor=p2') ? { items: [entry(1)], next_cursor: null } : { items: [entry(2)], next_cursor: 'p2' } }),
    });
    renderWithProviders(<App />, { route: '/admin/audit' });
    expect(await screen.findByText('{"reason":"phishing kit"}')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Load older entries' }));
    await waitFor(() => expect(screen.getAllByText('link.disable')).toHaveLength(2));
    expect(calls.some((c) => c.url.includes('cursor=p2'))).toBe(true);
    expect(screen.queryByRole('button', { name: 'Load older entries' })).not.toBeInTheDocument();
  });

  it('shows platform analytics and users from the admin endpoints', async () => {
    mockApi({
      'GET /api/auth/me': admin,
      'GET /api/admin/analytics/overview': {
        body: { totals: { human: 4321, email_scanner: 1, link_preview: 1, crawler: 1, other_bot: 1, refused: 0, unique_visitor_days: 10 }, top_links: [], campaigns: [], alerts_open: 0 },
      },
      'GET /api/admin/users': { body: { items: [{ ...testUser, id: 2, email: 'ravi@example.com', created_at: '2026-10-01T00:00:00Z', link_count: 17 }], next_cursor: null } },
      'GET /api/admin/links': empty,
    });
    renderWithProviders(<App />, { route: '/admin' });
    expect(within(await screen.findByLabelText('Key numbers')).getByText('4,321')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('link', { name: 'Users' }));
    const row = (await screen.findByText('ravi@example.com')).closest('tr') as HTMLElement;
    expect(within(row).getByText('17')).toBeInTheDocument();
  });
});
