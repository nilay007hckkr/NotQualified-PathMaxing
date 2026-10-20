import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { LinkAnalytics, TrustReport } from '../api/types';
import { sampleLink } from '../test/fixtures';
import { mockApi, renderWithProviders } from '../test/utils';
import { LinkDetailsPage } from './LinkDetailsPage';

afterEach(() => {
  vi.unstubAllGlobals();
});

const analytics: LinkAnalytics = {
  link_id: 42,
  tz: 'Asia/Kolkata',
  interval: 'day',
  totals: { human: 120, email_scanner: 31, link_preview: 14, crawler: 5, other_bot: 2, refused: 9, unique_visitor_days: 98 },
  series: [{ t: '2026-10-18T00:00:00+05:30', human: 10, bots: 3, refused: 1 }],
  by_device: { mobile: 80, desktop: 35, tablet: 5 },
  by_browser: { Chrome: 70 },
  by_os: { Android: 61 },
  by_referrer: [{ host: 'instagram.com', clicks: 40 }],
  by_country: [{ country: 'IN', clicks: 110 }],
  by_source: { link: 100, qr: 21 },
  outcomes: { redirected: 171, expired: 0, cap_reached: 3, blocked_admin: 0, blocked_domain: 0, quarantined: 0, inactive: 0, not_started: 0, password_required: 6 },
};

const trust: TrustReport = {
  state: 'ok',
  last_checked_at: '2026-10-20T08:00:00Z',
  next_check_at: '2026-10-20T14:00:00Z',
  checks: [{ checked_at: '2026-10-20T08:00:00Z', http_status: 200, final_url: 'https://shop.example/sale', final_domain: 'shop.example', hops: 1, verdict: 'ok', sources: ['safe_browsing', 'urlhaus'] }],
};

function renderDetails() {
  return renderWithProviders(
    <Routes>
      <Route path="/links/:id" element={<LinkDetailsPage />} />
      <Route path="/links" element={<p>All links</p>} />
    </Routes>,
    { route: '/links/42' },
  );
}

describe('link details', () => {
  it('shows facts, analytics from the API and the trust timeline', async () => {
    const { calls } = mockApi({
      'GET /api/links/42': { body: { link: sampleLink } },
      'GET /api/analytics/links/42': { body: analytics },
      'GET /api/links/42/trust': { body: trust },
    });
    renderDetails();

    expect(await screen.findByRole('heading', { name: 'Diwali sale' })).toBeInTheDocument();
    expect(screen.getByText('12 of 100 human clicks used')).toBeInTheDocument();
    expect(screen.getByAltText('QR code for https://pm.example/diwali')).toHaveAttribute('src', '/api/links/42/qr?format=svg&size=512');

    const kpis = await screen.findByLabelText('Visits by traffic class');
    expect(within(within(kpis).getByText('Email scanner (probable)').closest('.kpi') as HTMLElement).getByText('31')).toBeInTheDocument();
    expect(within(within(kpis).getByText('Unique visitor-days').closest('.kpi') as HTMLElement).getByText('98')).toBeInTheDocument();
    expect(within(screen.getByText('Operating system').parentElement!).getByText('61')).toBeInTheDocument();
    expect(within(screen.getByText('Source').parentElement!).getByText('QR scan')).toBeInTheDocument();
    expect(screen.getByText('India (IN)')).toBeInTheDocument();
    expect(within(screen.getByText('Click cap reached (410)').closest('tr')!).getByText('3')).toBeInTheDocument();

    expect(await screen.findByText(/checked with safe_browsing, urlhaus/)).toBeInTheDocument();
    expect(screen.getByText('20 Oct 2026, 7:30 pm IST')).toBeInTheDocument(); // next check, in IST

    const req = new URL(calls.find((c) => c.url.startsWith('/api/analytics/links/42'))!.url, 'http://x').searchParams;
    expect(req.get('tz')).toBe('Asia/Kolkata');
    expect(req.get('interval')).toBe('day');
  });

  it('queues a re-scan and shows the new state', async () => {
    let scanned = false;
    const { calls } = mockApi({
      'GET /api/links/42': { body: { link: sampleLink } },
      'GET /api/analytics/links/42': { body: analytics },
      'GET /api/links/42/trust': () => ({ body: scanned ? { ...trust, state: 'quarantined' } : trust }),
      'POST /api/links/42/rescan': () => {
        scanned = true;
        return { status: 202, body: { queued: true } };
      },
    });
    renderDetails();
    await userEvent.click(await screen.findByRole('button', { name: 'Re-scan now' }));
    expect(await screen.findByText(/Re-scan queued/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST' && c.url === '/api/links/42/rescan')).toBe(true);
    const trustCard = screen.getByRole('region', { name: 'Destination trust' });
    expect(await within(trustCard).findByText('Quarantined')).toBeInTheDocument();
  });

  it('pauses the link and deletes it only after confirming', async () => {
    const { calls } = mockApi({
      'GET /api/links/42': { body: { link: sampleLink } },
      'GET /api/analytics/links/42': { body: analytics },
      'GET /api/links/42/trust': { body: trust },
      'POST /api/links/42/pause': { body: { link: { ...sampleLink, status: 'paused' } } },
      'DELETE /api/links/42': { status: 204 },
    });
    renderDetails();
    await userEvent.click(await screen.findByRole('button', { name: 'Pause' }));
    expect(await screen.findByRole('button', { name: 'Resume' })).toBeInTheDocument();
    expect(screen.getAllByText('Paused').length).toBeGreaterThan(0);

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: 'Yes, delete' }));
    expect(await screen.findByText('All links')).toBeInTheDocument();
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/links/42')).toBe(true));
  });

  it('treats a 404 as not found, without saying whose link it is', async () => {
    mockApi({});
    renderDetails();
    expect(await screen.findByText('This link doesn’t exist, or isn’t yours.')).toBeInTheDocument();
  });
});
