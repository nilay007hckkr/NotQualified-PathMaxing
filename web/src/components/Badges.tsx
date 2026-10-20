import type { Link, LinkStatus, TrustState } from '../api/types';

const STATUS: Record<LinkStatus, { label: string; tone: string }> = {
  active: { label: 'Active', tone: 'ok' },
  paused: { label: 'Paused', tone: 'warn' },
  archived: { label: 'Archived', tone: 'muted' },
  disabled_admin: { label: 'Disabled by admin', tone: 'danger' },
};

export const TRUST: Record<TrustState, { label: string; tone: string; help: string }> = {
  ok: { label: 'Trust OK', tone: 'ok', help: 'The last check found the destination reachable and clean.' },
  degraded: { label: 'Degraded', tone: 'warn', help: 'The destination failed its last check (down or erroring).' },
  changed: { label: 'Changed', tone: 'warn', help: 'The destination now ends somewhere different from when it was published.' },
  quarantined: { label: 'Quarantined', tone: 'danger', help: 'A threat feed flagged the destination; visitors see a warning page.' },
};

export const STATUS_OPTIONS = Object.entries(STATUS).map(([value, s]) => ({ value: value as LinkStatus, label: s.label }));
export const TRUST_OPTIONS = Object.entries(TRUST).map(([value, s]) => ({ value: value as TrustState, label: s.label }));

/** The status as a labelled badge; expiry is computed per visit, so it shows next to the stored status. */
export function StatusBadge({ link }: { link: Pick<Link, 'status' | 'is_expired'> }) {
  const s = STATUS[link.status] ?? { label: link.status, tone: 'muted' };
  return (
    <span className="badges">
      <span className={`badge badge-${s.tone}`}>{s.label}</span>
      {link.is_expired && <span className="badge badge-muted">Expired</span>}
    </span>
  );
}

export function TrustBadge({ state }: { state: TrustState }) {
  const t = TRUST[state] ?? { label: state, tone: 'muted', help: '' };
  return (
    <span className={`badge badge-${t.tone}`} title={t.help}>
      {t.label}
    </span>
  );
}
