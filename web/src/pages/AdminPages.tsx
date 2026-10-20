import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { NavLink, Outlet, Link as RouterLink } from 'react-router';
import { api } from '../api/client';
import type { Link, LinkStatus, TrustState } from '../api/types';
import { STATUS_OPTIONS, StatusBadge, TRUST_OPTIONS, TrustBadge } from '../components/Badges';
import { Field, SelectField } from '../components/Field';
import { OverviewBody } from '../components/Overview';
import { LiveBadge, PeriodPicker } from '../components/PeriodPicker';
import { EmptyState, ErrorState, Loading, describeError } from '../components/States';
import { TZ, formatDateTime, formatNumber, periodRange, type PeriodKey } from '../lib/format';
import './admin.css';
import { LIVE_MS } from './DashboardPage';

const adminKey = ['admin'] as const;

/** The admin area's own sub-navigation; the route itself is wrapped in RequireAuth adminOnly. */
export function AdminLayout() {
  return (
    <section aria-labelledby="admin-title">
      <div className="page-head">
        <div>
          <p className="kicker">Admins only</p>
          <h1 id="admin-title">Admin</h1>
          <p className="muted">Platform-wide view. Every takedown and blocklist change is written to the audit log.</p>
        </div>
      </div>
      <nav aria-label="Admin" className="subnav">
        <NavLink to="/admin" end>
          Platform
        </NavLink>
        <NavLink to="/admin/abuse">Abuse queue</NavLink>
        <NavLink to="/admin/links">All links</NavLink>
        <NavLink to="/admin/blocklist">Blocklist</NavLink>
        <NavLink to="/admin/users">Users</NavLink>
        <NavLink to="/admin/audit">Audit log</NavLink>
      </nav>
      <Outlet />
    </section>
  );
}

export function AdminOverviewPage() {
  const [period, setPeriod] = useState<PeriodKey>('7d');
  const overview = useQuery({
    queryKey: [...adminKey, 'overview', period],
    queryFn: () => api.admin.overview({ ...periodRange(period), tz: TZ }),
    refetchInterval: LIVE_MS,
    placeholderData: keepPreviousData,
  });
  return (
    <>
      <div className="toolbar">
        <PeriodPicker value={period} onChange={setPeriod} />
        <LiveBadge updatedAt={overview.dataUpdatedAt} fetching={overview.isFetching} />
      </div>
      {overview.isPending ? (
        <Loading label="Loading platform analytics" />
      ) : overview.isError && !overview.data ? (
        <ErrorState error={overview.error} onRetry={() => overview.refetch()} />
      ) : (
        <OverviewBody data={overview.data} linkTo={(id, code) => <RouterLink to={`/links/${id}`}>/{code}</RouterLink>} />
      )}
    </>
  );
}

/** Disable needs a reason, because it is shown in the audit log and explains a 451 later. */
function TakedownControl({ link }: { link: Link }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [touched, setTouched] = useState(false);
  const done = () => {
    setOpen(false);
    setReason('');
    setTouched(false);
    qc.invalidateQueries({ queryKey: adminKey });
    qc.invalidateQueries({ queryKey: ['links'] });
  };
  const disable = useMutation({ mutationFn: () => api.admin.disable(link.id, reason.trim()), onSuccess: done });
  const enable = useMutation({ mutationFn: () => api.admin.enable(link.id), onSuccess: done });
  const error = disable.error ?? enable.error;

  if (link.status === 'disabled_admin') {
    return (
      <div className="actions">
        <button type="button" className="btn btn-secondary btn-small" disabled={enable.isPending} onClick={() => enable.mutate()} aria-label={`Enable /${link.code}`}>
          Enable
        </button>
        {error && <span className="field-error">{describeError(error)}</span>}
      </div>
    );
  }
  if (!open) {
    return (
      <button type="button" className="btn btn-danger btn-small" onClick={() => setOpen(true)} aria-label={`Disable /${link.code}`}>
        Disable
      </button>
    );
  }
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (reason.trim()) disable.mutate();
  };
  return (
    <form className="takedown" onSubmit={onSubmit} noValidate>
      <Field
        label={`Reason for disabling /${link.code}`}
        value={reason}
        autoFocus
        error={touched && !reason.trim() ? 'Give a reason; it goes into the audit log.' : error ? describeError(error) : undefined}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="actions">
        <button type="submit" className="btn btn-danger btn-small" disabled={disable.isPending}>
          Disable link
        </button>
        <button type="button" className="btn btn-secondary btn-small" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function AdminLinkTable({ links }: { links: Link[] }) {
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th scope="col">Link</th>
            <th scope="col">Destination</th>
            <th scope="col">Status</th>
            <th scope="col">Trust</th>
            <th scope="col">Last check</th>
            <th scope="col">Action</th>
          </tr>
        </thead>
        <tbody>
          {links.map((l) => (
            <tr key={l.id}>
              <td className="mono">
                <RouterLink to={`/links/${l.id}`}>/{l.code}</RouterLink>
              </td>
              <td>
                <span className="truncate" title={l.destination}>
                  {l.destination}
                </span>
              </td>
              <td>
                <StatusBadge link={l} />
              </td>
              <td>
                <TrustBadge state={l.trust_state} />
              </td>
              <td>{formatDateTime(l.last_checked_at)}</td>
              <td>
                <TakedownControl link={l} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function AbuseQueuePage() {
  const abuse = useQuery({ queryKey: [...adminKey, 'abuse'], queryFn: () => api.admin.abuse(), refetchInterval: LIVE_MS });
  return (
    <section className="card" aria-labelledby="abuse-title">
      <h2 id="abuse-title">Abuse queue</h2>
      <p className="muted">Links the trust scanner marked quarantined, changed or degraded, and links an admin disabled.</p>
      {abuse.isPending ? (
        <Loading label="Loading abuse queue" />
      ) : abuse.isError ? (
        <ErrorState error={abuse.error} onRetry={() => abuse.refetch()} />
      ) : abuse.data.items.length === 0 ? (
        <EmptyState title="Nothing needs review." />
      ) : (
        <AdminLinkTable links={abuse.data.items} />
      )}
    </section>
  );
}

export function AdminLinksPage() {
  const [q, setQ] = useState('');
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState<LinkStatus | ''>('');
  const [trust, setTrust] = useState<TrustState | ''>('');
  const filters = { q: search || undefined, status: status || undefined, trust_state: trust || undefined, limit: 50 };
  const links = useInfiniteQuery({
    queryKey: [...adminKey, 'links', filters],
    queryFn: ({ pageParam }) => api.admin.links({ ...filters, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    placeholderData: keepPreviousData,
  });
  const rows = links.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="card" aria-labelledby="all-links-title">
      <h2 id="all-links-title">All links</h2>
      <form
        className="toolbar"
        role="search"
        aria-label="Filter all links"
        onSubmit={(e) => {
          e.preventDefault();
          setSearch(q.trim());
        }}
      >
        <Field label="Search" type="search" value={q} placeholder="Code, title or destination" onChange={(e) => setQ(e.target.value)} />
        <SelectField label="Status" value={status} placeholder="Any status" options={STATUS_OPTIONS} onChange={(e) => setStatus(e.target.value as LinkStatus | '')} />
        <SelectField label="Trust" value={trust} placeholder="Any trust state" options={TRUST_OPTIONS} onChange={(e) => setTrust(e.target.value as TrustState | '')} />
        <button type="submit" className="btn btn-secondary">
          Search
        </button>
      </form>
      {links.isPending ? (
        <Loading label="Loading links" />
      ) : links.isError && rows.length === 0 ? (
        <ErrorState error={links.error} onRetry={() => links.refetch()} />
      ) : rows.length === 0 ? (
        <EmptyState title="No links match." />
      ) : (
        <>
          <AdminLinkTable links={rows} />
          {links.hasNextPage && (
            <div className="list-more">
              <button type="button" className="btn btn-secondary" disabled={links.isFetchingNextPage} onClick={() => links.fetchNextPage()}>
                Load more
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}

export function BlocklistPage() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: [...adminKey, 'blocklist'], queryFn: () => api.admin.blocklist() });
  const [domain, setDomain] = useState('');
  const [reason, setReason] = useState('');
  const [touched, setTouched] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: adminKey });
  const add = useMutation({
    mutationFn: () => api.admin.block(domain.trim().toLowerCase(), reason.trim()),
    onSuccess: () => {
      setDomain('');
      setReason('');
      setTouched(false);
      refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (d: string) => api.admin.unblock(d),
    onSuccess: () => {
      setConfirming(null);
      refresh();
    },
  });

  // Hosts only: a scheme or path here is a common paste mistake.
  const domainOk = /^(?!-)[a-z0-9-]+(\.[a-z0-9-]+)+$/i.test(domain.trim());
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (domainOk && reason.trim()) add.mutate();
  };

  return (
    <div className="grid-2">
      <form className="card" onSubmit={onSubmit} noValidate aria-labelledby="block-title">
        <h2 id="block-title">Block a domain</h2>
        <Field
          label="Domain"
          value={domain}
          placeholder="evil.example"
          hint="Blocking a domain also blocks all of its subdomains (evil.example blocks login.evil.example). Matching links are refused with 451 from their next visit."
          error={touched && !domainOk ? 'Enter a domain name only, like evil.example (no https:// or path).' : undefined}
          onChange={(e) => setDomain(e.target.value)}
        />
        <Field label="Reason" value={reason} error={touched && !reason.trim() ? 'Give a reason; it goes into the audit log.' : undefined} onChange={(e) => setReason(e.target.value)} />
        {add.isError && (
          <p className="form-error" role="alert">
            {describeError(add.error)}
          </p>
        )}
        <button type="submit" className="btn btn-danger" disabled={add.isPending}>
          Block domain
        </button>
      </form>

      <section className="card" aria-labelledby="blocked-title">
        <h2 id="blocked-title">Blocked domains</h2>
        {remove.isError && (
          <p className="form-error" role="alert">
            {describeError(remove.error)}
          </p>
        )}
        {list.isPending ? (
          <Loading label="Loading blocklist" />
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={() => list.refetch()} />
        ) : list.data.items.length === 0 ? (
          <EmptyState title="No blocked domains." />
        ) : (
          <ul className="plain-list">
            {list.data.items.map((b) => (
              <li key={b.domain}>
                <div>
                  <strong className="mono">{b.domain}</strong> <span className="muted">and subdomains</span>
                  <div className="muted">
                    {b.reason} · {formatDateTime(b.created_at)}
                  </div>
                </div>
                {confirming === b.domain ? (
                  <span className="confirm" role="group" aria-label={`Confirm unblocking ${b.domain}`}>
                    <button type="button" className="btn btn-danger btn-small" disabled={remove.isPending} onClick={() => remove.mutate(b.domain)}>
                      Unblock
                    </button>
                    <button type="button" className="btn btn-secondary btn-small" onClick={() => setConfirming(null)}>
                      Cancel
                    </button>
                  </span>
                ) : (
                  <button type="button" className="btn btn-secondary btn-small" onClick={() => setConfirming(b.domain)} aria-label={`Remove ${b.domain}`}>
                    Remove
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

export function UsersPage() {
  const users = useQuery({ queryKey: [...adminKey, 'users'], queryFn: () => api.admin.users() });
  return (
    <section className="card" aria-labelledby="users-title">
      <h2 id="users-title">Users</h2>
      {users.isPending ? (
        <Loading label="Loading users" />
      ) : users.isError ? (
        <ErrorState error={users.error} onRetry={() => users.refetch()} />
      ) : users.data.items.length === 0 ? (
        <EmptyState title="No users." />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Email</th>
                <th scope="col">Name</th>
                <th scope="col">Role</th>
                <th scope="col" className="num">
                  Links
                </th>
                <th scope="col">Joined</th>
              </tr>
            </thead>
            <tbody>
              {users.data.items.map((u) => (
                <tr key={u.id}>
                  <td>{u.email}</td>
                  <td>{u.name}</td>
                  <td>
                    <span className={`badge ${u.role === 'admin' ? 'badge-admin' : 'badge-muted'}`}>{u.role}</span>
                  </td>
                  <td className="num">{formatNumber(u.link_count)}</td>
                  <td>{formatDateTime(u.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

export function AuditPage() {
  const audit = useInfiniteQuery({
    queryKey: [...adminKey, 'audit'],
    queryFn: ({ pageParam }) => api.admin.audit(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const rows = audit.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="card" aria-labelledby="audit-title">
      <h2 id="audit-title">Audit log</h2>
      {audit.isPending ? (
        <Loading label="Loading audit log" />
      ) : audit.isError && rows.length === 0 ? (
        <ErrorState error={audit.error} onRetry={() => audit.refetch()} />
      ) : rows.length === 0 ? (
        <EmptyState title="No admin actions yet." />
      ) : (
        <>
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">When</th>
                  <th scope="col">Actor</th>
                  <th scope="col">Action</th>
                  <th scope="col">Entity</th>
                  <th scope="col">Detail</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((a) => (
                  <tr key={a.id}>
                    <td>{formatDateTime(a.ts)}</td>
                    <td>{a.actor_id === null ? 'system' : `user #${a.actor_id}`}</td>
                    <td className="mono">{a.action}</td>
                    <td>
                      {a.entity_type === 'link' ? <RouterLink to={`/links/${a.entity_id}`}>link #{a.entity_id}</RouterLink> : `${a.entity_type} #${a.entity_id}`}
                    </td>
                    <td className="mono detail">{a.detail && Object.keys(a.detail).length ? JSON.stringify(a.detail) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {audit.hasNextPage && (
            <div className="list-more">
              <button type="button" className="btn btn-secondary" disabled={audit.isFetchingNextPage} onClick={() => audit.fetchNextPage()}>
                Load older entries
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}
