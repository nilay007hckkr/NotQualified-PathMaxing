import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from '../App';
import type { Overview } from '../api/types';
import { mockApi, renderWithProviders, testUser } from '../test/utils';

afterEach(() => {
  vi.unstubAllGlobals();
});

const overview: Overview = {
  totals: { human: 1234, email_scanner: 30, link_preview: 14, crawler: 5, other_bot: 2, refused: 9, unique_visitor_days: 987 },
  top_links: [{ link_id: 42, code: 'diwali', human: 777 }],
  campaigns: [{ id: 3, name: 'Diwali launch', human: 555 }],
  alerts_open: 2,
};

const me = { body: { user: testUser } };

/** The KPI tile whose label is label; tables on the page repeat some of the same words. */
async function tile(label: string) {
  const list = await screen.findByLabelText('Key numbers');
  return within(list).getByText(label).closest('.kpi') as HTMLElement;
}

describe('dashboard', () => {
  it('shows the numbers the API returns, in IST, with alerts', async () => {
    const { calls } = mockApi({
      'GET /api/auth/me': me,
      'GET /api/analytics/overview': { body: overview },
      'GET /api/alerts': { body: { items: [{ id: 1, link_id: 42, kind: 'near_cap', message: 'diwali has used 95 of 100 clicks', created_at: '2026-10-20T08:00:00Z' }] } },
    });
    renderWithProviders(<App />, { route: '/' });

    expect(within(await tile('Human clicks')).getByText('1,234')).toBeInTheDocument();
    // Bots are the four bot classes added up: 30 + 14 + 5 + 2.
    expect(within(await tile('Bot visits')).getByText('51')).toBeInTheDocument();
    expect(within(await tile('Unique visitor-days')).getByText('987')).toBeInTheDocument();
    expect(within(await tile('Open alerts')).getByText('2')).toBeInTheDocument();
    const top = screen.getByRole('region', { name: 'Top links' });
    expect(within(top).getByText('/diwali')).toBeInTheDocument();
    expect(within(top).getByText('777')).toBeInTheDocument();
    expect(screen.getByText('Diwali launch')).toBeInTheDocument();
    expect(await screen.findByText('diwali has used 95 of 100 clicks')).toBeInTheDocument();
    expect(screen.getByText('Near its click cap')).toBeInTheDocument();
    expect(screen.getByText('20 Oct 2026, 1:30 pm IST')).toBeInTheDocument();

    const req = calls.find((c) => c.url.startsWith('/api/analytics/overview'))!;
    const qs = new URL(req.url, 'http://x').searchParams;
    expect(qs.get('tz')).toBe('Asia/Kolkata');
    const span = Date.parse(qs.get('to')!) - Date.parse(qs.get('from')!);
    expect(span).toBe(7 * 24 * 3600_000);
    expect(calls.find((c) => c.url.startsWith('/api/alerts'))!.url).toBe('/api/alerts?status=open');
  });

  it('asks for a new window when the period changes', async () => {
    const { calls } = mockApi({
      'GET /api/auth/me': me,
      'GET /api/analytics/overview': { body: overview },
      'GET /api/alerts': { body: { items: [] } },
    });
    renderWithProviders(<App />, { route: '/' });
    await tile('Human clicks');
    await userEvent.click(screen.getByLabelText('Last 24 hours'));
    await waitFor(() => {
      const spans = calls
        .filter((c) => c.url.startsWith('/api/analytics/overview'))
        .map((c) => new URL(c.url, 'http://x').searchParams)
        .map((q) => Date.parse(q.get('to')!) - Date.parse(q.get('from')!));
      expect(spans).toContain(24 * 3600_000);
    });
    expect(screen.getByText('No open alerts.')).toBeInTheDocument();
  });

  it('shows empty states instead of zeros it did not get', async () => {
    mockApi({
      'GET /api/auth/me': me,
      'GET /api/analytics/overview': { body: { ...overview, totals: { human: 0, email_scanner: 0, link_preview: 0, crawler: 0, other_bot: 0, refused: 0, unique_visitor_days: 0 }, top_links: [], campaigns: [], alerts_open: 0 } },
      'GET /api/alerts': { body: { items: [] } },
    });
    renderWithProviders(<App />, { route: '/' });
    expect(await screen.findByText('No human clicks in this period.')).toBeInTheDocument();
    expect(screen.getByText('No clicks in this period yet.')).toBeInTheDocument();
    expect(screen.getByText('No campaign traffic in this period.')).toBeInTheDocument();
  });

  it('shows an error with a retry when analytics fail', async () => {
    mockApi({
      'GET /api/auth/me': me,
      'GET /api/analytics/overview': { status: 500, body: { error: { code: 'INTERNAL', message: 'boom' } } },
      'GET /api/alerts': { body: { items: [] } },
    });
    renderWithProviders(<App />, { route: '/' });
    const alert = await screen.findByText('The server ran into a problem. Try again in a moment.');
    expect(alert.closest('[role="alert"]')).not.toBeNull();
    expect(screen.queryByLabelText('Key numbers')).not.toBeInTheDocument();
  });
});
