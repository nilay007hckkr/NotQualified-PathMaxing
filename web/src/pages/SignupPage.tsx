import { useState, type FormEvent } from 'react';
import { Link, Navigate, useNavigate } from 'react-router';
import { ApiError } from '../api/client';
import { useMe, useSignup } from '../auth/useAuth';
import { Field } from '../components/Field';
import { describeError } from '../components/States';
import { AuthCard } from './LoginPage';

export const MIN_PASSWORD = 8;

export function SignupPage() {
  const me = useMe();
  const signup = useSignup();
  const navigate = useNavigate();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [touched, setTouched] = useState(false);

  if (me.data) return <Navigate to="/" replace />;

  const emailError = touched && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim()) ? 'Enter an email address like you@example.com.' : undefined;
  const passwordError = touched && password.length < MIN_PASSWORD ? `Use at least ${MIN_PASSWORD} characters.` : undefined;
  const nameError = touched && !name.trim() ? 'Tell us what to call you.' : undefined;

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!name.trim() || password.length < MIN_PASSWORD || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) return;
    signup.mutate({ name: name.trim(), email: email.trim(), password }, { onSuccess: () => navigate('/', { replace: true }) });
  };

  let error: string | undefined;
  if (signup.error) {
    error = signup.error instanceof ApiError && signup.error.status === 409
      ? 'An account with this email already exists. Log in instead.'
      : describeError(signup.error);
  }

  return (
    <AuthCard title="Create your account" subtitle="Every account gets its own workspace for links, campaigns and analytics.">
      <form onSubmit={onSubmit} noValidate>
        <Field label="Name" name="name" autoComplete="name" required value={name} error={nameError} onChange={(e) => setName(e.target.value)} />
        <Field label="Email" type="email" name="email" autoComplete="email" required value={email} error={emailError} onChange={(e) => setEmail(e.target.value)} />
        <Field
          label="Password"
          type="password"
          name="password"
          autoComplete="new-password"
          required
          minLength={MIN_PASSWORD}
          value={password}
          hint={`At least ${MIN_PASSWORD} characters.`}
          error={passwordError}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" className="btn btn-primary btn-block" disabled={signup.isPending}>
          {signup.isPending ? 'Creating account…' : 'Create account'}
        </button>
      </form>
      <p className="auth-switch">
        Already have an account? <Link to="/login">Log in</Link>
      </p>
    </AuthCard>
  );
}
