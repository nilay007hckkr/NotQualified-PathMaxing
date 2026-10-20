import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ImportJob } from '../api/types';
import { sampleLink } from '../test/fixtures';
import { mockApi, renderWithProviders } from '../test/utils';
import { BioPagesPage } from './BioPagesPage';
import { CampaignDetailsPage, CampaignsPage } from './CampaignsPage';
import { ImportPage } from './ImportPage';

afterEach(() => {
  vi.unstubAllGlobals();
});

const preview: ImportJob = {
  id: 7,
  status: 'preview',
  total: 4,
  valid: 2,
  invalid: 2,
  rows: [
    { row: 2, alias: 'promo', destination: 'https://shop.example/a', status: 'valid', errors: [] },
    { row: 3, alias: 'promo', destination: 'https://shop.example/b', status: 'invalid', errors: ['DUP_IN_FILE'] },
    { row: 4, alias: 'winter', destination: 'https://shop.example/c', status: 'invalid', errors: ['UNKNOWN_CAMPAIGN', 'SCHEME_NOT_ALLOWED'] },
    { row: 5, alias: '', destination: 'https://shop.example/d', status: 'valid', errors: [] },
  ],
};

const csv = () => new File(['destination,alias\nhttps://shop.example/a,promo\n'], 'links.csv', { type: 'text/csv' });

describe('bulk import', () => {
  it('previews planted errors per row, then imports only the valid rows', async () => {
    const { calls } = mockApi({
      'POST /api/imports': { status: 201, body: preview },
      'POST /api/imports/7/commit': { body: { created: 1, skipped: [{ row: 5, errors: ['ALIAS_TAKEN'] }] } },
    });
    renderWithProviders(<ImportPage />, { route: '/import' });
    await userEvent.upload(screen.getByLabelText('CSV file'), csv());
    await userEvent.click(screen.getByRole('button', { name: 'Upload and preview' }));

    const summary = await screen.findByLabelText('Import summary');
    expect(within(within(summary).getByText('Invalid').closest('.kpi') as HTMLElement).getByText('2')).toBeInTheDocument();
    const row4 = screen.getByText('winter').closest('tr') as HTMLElement;
    expect(within(row4).getByText('UNKNOWN_CAMPAIGN')).toBeInTheDocument();
    expect(within(row4).getByText('SCHEME_NOT_ALLOWED')).toBeInTheDocument();
    expect(within(screen.getAllByText('promo')[1].closest('tr') as HTMLElement).getByText('DUP_IN_FILE')).toBeInTheDocument();

    const sent = calls.find((c) => c.url === '/api/imports')!.body as FormData;
    expect(sent).toBeInstanceOf(FormData);
    expect((sent.get('file') as File).name).toBe('links.csv');

    await userEvent.click(screen.getByLabelText('Only rows with problems'));
    expect(screen.queryByText('https://shop.example/a')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Import 2 valid rows' }));
    expect(await screen.findByText('Import finished')).toBeInTheDocument();
    expect(screen.getByText(/Row 5: ALIAS_TAKEN/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST' && c.url === '/api/imports/7/commit')).toBe(true);
  });

  it('explains a file that is too large', async () => {
    mockApi({ 'POST /api/imports': { status: 413, body: { error: { code: 'TOO_LARGE', message: 'request body too large' } } } });
    renderWithProviders(<ImportPage />, { route: '/import' });
    await userEvent.upload(screen.getByLabelText('CSV file'), csv());
    await userEvent.click(screen.getByRole('button', { name: 'Upload and preview' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('at most 1,000 rows and 1 MB');
  });
});

const campaign = { id: 3, name: 'Diwali launch', utm_source: 'instagram', utm_medium: 'social', utm_campaign: 'diwali', link_count: 4, created_at: '2026-10-01T00:00:00Z' };

describe('campaigns', () => {
  it('creates a campaign with UTM fields', async () => {
    const { calls } = mockApi({
      'GET /api/campaigns': { body: { items: [campaign], next_cursor: null } },
      'GET /api/tags': { body: { items: [{ id: 1, name: 'festive', color: '#ff9900' }], next_cursor: null } },
      'POST /api/campaigns': { status: 201, body: { campaign: { ...campaign, id: 4, name: 'Winter' } } },
    });
    renderWithProviders(<CampaignsPage />, { route: '/campaigns' });
    expect(await screen.findByRole('link', { name: 'Diwali launch' })).toHaveAttribute('href', '/campaigns/3');
    expect(screen.getByText('festive')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Create campaign' }));
    expect(screen.getByText('Give the campaign a name.')).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Name'), 'Winter');
    await userEvent.type(screen.getByLabelText('utm_source'), 'newsletter');
    await userEvent.click(screen.getByRole('button', { name: 'Create campaign' }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === 'POST' && c.url === '/api/campaigns')?.body).toEqual({ name: 'Winter', utm_source: 'newsletter', utm_medium: '', utm_campaign: '' }),
    );
  });

  it('shows campaign analytics and top links from the API', async () => {
    mockApi({
      'GET /api/campaigns/3': { body: { campaign } },
      'GET /api/analytics/campaigns/3': {
        body: {
          campaign_id: 3,
          tz: 'Asia/Kolkata',
          interval: 'day',
          totals: { human: 321, email_scanner: 4, link_preview: 6, crawler: 0, other_bot: 1, refused: 2, unique_visitor_days: 300 },
          series: [],
          top_links: [{ link_id: 42, code: 'diwali', human: 210 }],
        },
      },
    });
    renderWithProviders(
      <Routes>
        <Route path="/campaigns/:id" element={<CampaignDetailsPage />} />
      </Routes>,
      { route: '/campaigns/3' },
    );
    expect(await screen.findByRole('heading', { name: 'Diwali launch' })).toBeInTheDocument();
    const kpis = await screen.findByLabelText('Campaign visits by traffic class');
    expect(within(kpis).getByText('321')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '/diwali' })).toHaveAttribute('href', '/links/42');
    expect(screen.getByText('210')).toBeInTheDocument();
  });
});

describe('bio pages', () => {
  it('creates a page with the chosen links and links to the public page', async () => {
    const { calls } = mockApi({
      'GET /api/bio-pages': { body: { items: [{ id: 1, slug: 'asha', title: 'Asha’s links', link_ids: [42] }], next_cursor: null } },
      'GET /api/links': { body: { items: [sampleLink], next_cursor: null } },
      'POST /api/bio-pages': { status: 201, body: { bio_page: { id: 2, slug: 'shop', title: 'Shop', link_ids: [42] } } },
    });
    renderWithProviders(<BioPagesPage />, { route: '/bio' });
    expect(await screen.findByRole('link', { name: '/bio/asha' })).toHaveAttribute('href', '/bio/asha');

    await userEvent.type(screen.getByLabelText('Address'), 'shop');
    await userEvent.type(screen.getByLabelText('Page title'), 'Shop');
    await userEvent.click(await screen.findByRole('checkbox', { name: /diwali/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Create page' }));
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ slug: 'shop', title: 'Shop', link_ids: [42] }));
  });
});
