import { useState, type FormEvent, type ReactNode } from 'react';
import { Link, Navigate, useLocation, useNavigate } from 'react-router';
import { ApiError } from '../api/client';
import { useLogin, useMe } from '../auth/useAuth';
import { Field } from '../components/Field';
import { describeError } from '../components/States';

export function LoginPage() {
  const me = useMe();
  const login = useLogin();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? '/';
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  if (me.data) return <Navigate to={from} replace />;

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate({ email: email.trim(), password }, { onSuccess: () => navigate(from, { replace: true }) });
  };

  let error: string | undefined;
  if (login.error) {
    error = login.error instanceof ApiError && login.error.status === 401
      ? 'That email and password don’t match an account.'
      : describeError(login.error);
  }

  return (
    <AuthCard title="Log in" subtitle="Manage your links and see who really clicked them.">
      <form onSubmit={onSubmit} noValidate>
        <Field label="Email" type="email" name="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <Field label="Password" type="password" name="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" className="btn btn-primary btn-block" disabled={login.isPending || !email || !password}>
          {login.isPending ? 'Logging in…' : 'Log in'}
        </button>
      </form>
      <p className="auth-switch">
        New here? <Link to="/signup">Create an account</Link>
      </p>
    </AuthCard>
  );
}

export function AuthCard({ title, subtitle, children }: { title: string; subtitle: string; children: ReactNode }) {
  return (
    <main className="auth-page" id="main">
      <div className="auth-card">
        <div className="brand brand-large">
          <img src={`${import.meta.env.BASE_URL}favicon.svg`} alt="" width={36} height={36} />
          <span>
            Path<b>Maxing</b>
          </span>
        </div>
        <p className="kicker">The accountable short link</p>
        <h1>{title}</h1>
        <p className="muted">{subtitle}</p>
        {children}
      </div>
    </main>
  );
}
