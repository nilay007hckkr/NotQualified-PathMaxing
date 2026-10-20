import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from '../App';
import { mockApi, renderWithProviders, testUser } from '../test/utils';

afterEach(() => {
  vi.unstubAllGlobals();
});

const signedOut = { status: 401, body: { error: { code: 'UNAUTHENTICATED', message: 'not logged in' } } };

describe('login', () => {
  it('sends a signed-out visitor to the login page, then back where they were going', async () => {
    let signedIn = false;
    const { calls } = mockApi({
      'GET /api/auth/me': () => (signedIn ? { body: { user: testUser } } : signedOut),
      'POST /api/auth/login': () => {
        signedIn = true;
        return { body: { user: testUser } };
      },
    });
    renderWithProviders(
      <Routes>
        <Route path="/*" element={<App />} />
      </Routes>,
      { route: '/' },
    );

    const email = await screen.findByLabelText('Email');
    await userEvent.type(email, '  asha@example.com ');
    await userEvent.type(screen.getByLabelText('Password'), 'correct horse');
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }));

    expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ email: 'asha@example.com', password: 'correct horse' });
  });

  it('explains a wrong password without saying which part was wrong', async () => {
    mockApi({
      'GET /api/auth/me': signedOut,
      'POST /api/auth/login': { status: 401, body: { error: { code: 'BAD_CREDENTIALS', message: 'invalid email or password' } } },
    });
    renderWithProviders(<App />, { route: '/login' });
    await userEvent.type(await screen.findByLabelText('Email'), 'asha@example.com');
    await userEvent.type(screen.getByLabelText('Password'), 'wrong');
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('That email and password don’t match an account.');
  });

  it('tells the user how long to wait when rate limited', async () => {
    mockApi({
      'GET /api/auth/me': signedOut,
      'POST /api/auth/login': { status: 429, body: { error: { code: 'RATE_LIMITED', message: 'too many' } }, headers: { 'Retry-After': '60' } },
    });
    renderWithProviders(<App />, { route: '/login' });
    await userEvent.type(await screen.findByLabelText('Email'), 'asha@example.com');
    await userEvent.type(screen.getByLabelText('Password'), 'whatever1');
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Try again in 60 seconds');
  });
});

describe('signup', () => {
  it('checks the form before sending it', async () => {
    const { calls } = mockApi({ 'GET /api/auth/me': signedOut });
    renderWithProviders(<App />, { route: '/signup' });
    await userEvent.type(await screen.findByLabelText('Email'), 'not-an-email');
    await userEvent.type(screen.getByLabelText('Password'), 'short');
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }));

    expect(screen.getByText('Tell us what to call you.')).toBeInTheDocument();
    expect(screen.getByText('Enter an email address like you@example.com.')).toBeInTheDocument();
    expect(screen.getByText('Use at least 8 characters.')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toHaveAttribute('aria-invalid', 'true');
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(0);
  });

  it('creates the account and signs the user in', async () => {
    const { calls } = mockApi({
      'GET /api/auth/me': signedOut,
      'POST /api/auth/signup': { status: 201, body: { user: testUser } },
    });
    renderWithProviders(<App />, { route: '/signup' });
    await userEvent.type(await screen.findByLabelText('Name'), 'Asha');
    await userEvent.type(screen.getByLabelText('Email'), 'asha@example.com');
    await userEvent.type(screen.getByLabelText('Password'), 'long enough');
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }));

    expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ name: 'Asha', email: 'asha@example.com', password: 'long enough' });
  });

  it('points an existing user to the login page', async () => {
    mockApi({
      'GET /api/auth/me': signedOut,
      'POST /api/auth/signup': { status: 409, body: { error: { code: 'EMAIL_TAKEN', message: 'email already registered' } } },
    });
    renderWithProviders(<App />, { route: '/signup' });
    await userEvent.type(await screen.findByLabelText('Name'), 'Asha');
    await userEvent.type(screen.getByLabelText('Email'), 'asha@example.com');
    await userEvent.type(screen.getByLabelText('Password'), 'long enough');
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('already exists');
  });
});

describe('session', () => {
  it('logs out and clears the session', async () => {
    let signedIn = true;
    mockApi({
      'GET /api/auth/me': () => (signedIn ? { body: { user: testUser } } : signedOut),
      'POST /api/auth/logout': () => {
        signedIn = false;
        return { status: 204 };
      },
    });
    renderWithProviders(<App />, { route: '/' });
    await userEvent.click(await screen.findByRole('button', { name: 'Log out' }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument());
  });

  it('offers a retry when the server is unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    renderWithProviders(<App />, { route: '/' });
    expect(await screen.findByRole('alert', {}, { timeout: 4000 })).toHaveTextContent('Could not reach the server');
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });
});
