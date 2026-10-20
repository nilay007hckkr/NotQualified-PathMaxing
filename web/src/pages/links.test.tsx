import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { sampleLink } from '../test/fixtures';
import { mockApi, renderWithProviders } from '../test/utils';
import { LinksPage } from './LinksPage';
import { NewLinkPage } from './NewLinkPage';

afterEach(() => {
  vi.unstubAllGlobals();
});



const httpWarning = { field: 'destination', code: 'HTTP_NOT_HTTPS', message: 'destination uses http, not https' };

async function fillStep1(destination: string, alias = '') {
  await userEvent.type(screen.getByLabelText('Destination URL'), destination);
  if (alias) await userEvent.type(screen.getByLabelText('Custom alias (optional)'), alias);
  await userEvent.click(screen.getByRole('button', { name: 'Check destination' }));
}

describe('creation wizard', () => {
  it('shows field errors by code and does not let the user continue', async () => {
    const { calls } = mockApi({
      'POST /api/links/validate': {
        body: {
          errors: [{ field: 'alias', code: 'ALIAS_TAKEN', message: 'alias is already taken' }],
          warnings: [httpWarning],
        },
      },
    });
    renderWithProviders(<NewLinkPage />);
    await fillStep1('http://shop.example/sale', 'Sale');

    const alias = screen.getByLabelText('Custom alias (optional)');
    await waitFor(() => expect(alias).toHaveAttribute('aria-invalid', 'true'));
    expect(screen.getByText(/Aliases ignore letter case/)).toBeInTheDocument();
    expect(alias.getAttribute('aria-describedby')).toContain('-error');
    expect(screen.queryByRole('button', { name: 'Continue' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: /Step 2/ })).not.toBeInTheDocument();
    expect(calls.find((c) => c.url === '/api/links/validate')?.body).toEqual({ destination: 'http://shop.example/sale', alias: 'Sale' });

    // Editing the alias clears the old verdict, so it must be checked again.
    await userEvent.type(alias, '2');
    expect(alias).not.toHaveAttribute('aria-invalid');
  });

  it('blocks an empty destination without calling the API', async () => {
    const { calls } = mockApi({});
    renderWithProviders(<NewLinkPage />);
    await userEvent.click(screen.getByRole('button', { name: 'Check destination' }));
    expect(screen.getByText('Enter the address people should land on.')).toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(0);
  });

  it('walks through options and review, sends UTC times, and shows the QR code', async () => {
    const { calls } = mockApi({
      'POST /api/links/validate': (_url, init) => {
        const body = JSON.parse(String(init.body));
        return { body: { errors: [], warnings: body.max_clicks === 1 ? [] : [httpWarning] } };
      },
      'POST /api/links': { status: 201, body: { link: sampleLink, warnings: [httpWarning] } },
    });
    renderWithProviders(<NewLinkPage />);
    await fillStep1('http://shop.example/sale', 'diwali');

    expect(await screen.findByText('Looks good.')).toBeInTheDocument();
    expect(screen.getByText('HTTP_NOT_HTTPS')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));

    expect(await screen.findByRole('heading', { name: 'Step 2: Options' })).toHaveFocus();
    // Campaign and tag endpoints are unavailable here, so their pickers stay hidden.
    expect(screen.queryByLabelText('Campaign')).not.toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Title'), 'Diwali sale');
    fireEvent.change(screen.getByLabelText('Expires at (IST)'), { target: { value: '2026-11-05T23:59' } });
    await userEvent.type(screen.getByLabelText('Click limit'), '100');
    await userEvent.type(screen.getByLabelText('Mobile destination'), 'https://m.shop.example/sale');
    await userEvent.click(screen.getByRole('button', { name: 'Review' }));

    expect(await screen.findByRole('heading', { name: 'Step 3: Review and publish' })).toBeInTheDocument();
    expect(screen.getByText('5 Nov 2026, 11:59 pm IST')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Publish link' }));

    expect(await screen.findByRole('heading', { name: 'Your link is live' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'https://pm.example/diwali' })).toBeInTheDocument();
    expect(screen.getByAltText('QR code for https://pm.example/diwali')).toHaveAttribute('src', '/api/links/42/qr?format=svg&size=512');
    expect(screen.getByRole('link', { name: 'Download PNG' })).toHaveAttribute('href', '/api/links/42/qr?format=png&size=512');

    const create = calls.find((c) => c.method === 'POST' && c.url === '/api/links')!;
    expect(create.body).toEqual({
      destination: 'http://shop.example/sale',
      alias: 'diwali',
      title: 'Diwali sale',
      starts_at: null,
      expires_at: '2026-11-05T18:29:00Z',
      max_clicks: 100,
      targets: [{ device: 'mobile', destination: 'https://m.shop.example/sale' }],
      campaign_id: null,
    });
  });

  it('sends a 422 from publish back to the step that owns the field', async () => {
    mockApi({
      'POST /api/links/validate': { body: { errors: [], warnings: [] } },
      'POST /api/links': {
        status: 422,
        body: { errors: [{ field: 'expires_at', code: 'EXPIRY_IN_PAST', message: 'expiry is in the past' }], warnings: [] },
      },
    });
    renderWithProviders(<NewLinkPage />);
    await fillStep1('https://shop.example');
    await userEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Review' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Publish link' }));

    expect(await screen.findByRole('heading', { name: 'Step 2: Options' })).toBeInTheDocument();
    expect(screen.getByLabelText('Expires at (IST)')).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByText('The expiry time is already in the past.')).toBeInTheDocument();
  });

  it('explains a 409 at publish as a taken alias and a 429 with its wait', async () => {
    let attempt = 0;
    mockApi({
      'POST /api/links/validate': { body: { errors: [], warnings: [] } },
      'POST /api/links': () =>
        ++attempt === 1
          ? { status: 429, body: { error: { code: 'RATE_LIMITED', message: 'slow down' } }, headers: { 'Retry-After': '30' } }
          : { status: 409, body: { error: { code: 'ALIAS_TAKEN', message: 'alias is already taken' } } },
    });
    renderWithProviders(<NewLinkPage />);
    await fillStep1('https://shop.example', 'sale');
    await userEvent.click(await screen.findByRole('button', { name: 'Continue' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Review' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Publish link' }));
    expect(await screen.findByText('Too many attempts. Try again in 30 seconds.')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Publish link' }));
    expect(await screen.findByRole('heading', { name: /Step 1/ })).toBeInTheDocument();
    expect(screen.getByLabelText('Custom alias (optional)')).toHaveAttribute('aria-invalid', 'true');
  });
});

describe('links list', () => {
  it('lists links with badges and sends filters to the API', async () => {
    const { calls } = mockApi({
      'GET /api/links': { body: { items: [sampleLink, { ...sampleLink, id: 43, code: 'promo', short_url: 'https://pm.example/promo', status: 'paused', trust_state: 'quarantined', max_clicks: null }], next_cursor: 'abc' } },
      'GET /api/campaigns': { body: { items: [{ id: 3, name: 'Diwali launch', utm_source: '', utm_medium: '', utm_campaign: '', link_count: 1, created_at: '2026-10-01T00:00:00Z' }], next_cursor: null } },
    });
    renderWithProviders(<LinksPage />, { route: '/links' });

    const row = (await screen.findByText('/promo')).closest('tr') as HTMLElement;
    expect(within(row).getByText('Paused')).toBeInTheDocument();
    expect(within(row).getByText('Quarantined')).toBeInTheDocument();
    expect(screen.getByText('12 / 100')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy https://pm.example/diwali' })).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByLabelText('Status'), 'paused');
    await userEvent.selectOptions(await screen.findByLabelText('Campaign'), '3');
    await userEvent.type(screen.getByLabelText('Search'), 'diw');
    await waitFor(() => expect(calls.some((c) => c.url.includes('q=diw') && c.url.includes('status=paused') && c.url.includes('campaign_id=3'))).toBe(true));

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(calls.some((c) => c.url.includes('cursor=abc'))).toBe(true));
  });

  it('shows an empty state for a new account', async () => {
    mockApi({ 'GET /api/links': { body: { items: [], next_cursor: null } } });
    renderWithProviders(<LinksPage />, { route: '/links' });
    expect(await screen.findByText('You have no links yet.')).toBeInTheDocument();
  });
});
