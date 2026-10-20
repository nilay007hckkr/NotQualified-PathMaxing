import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { Link as RouterLink, useNavigate, useParams } from 'react-router';
import { ApiError, api } from '../api/client';
import type { Link, Outcome, TrustCheck } from '../api/types';
import { StatusBadge, TRUST, TrustBadge } from '../components/Badges';
import { BarList, CLASS_ROWS, SeriesChart } from '../components/charts';
import { CopyButton } from '../components/CopyButton';
import { Kpi } from '../components/Overview';
import { LiveBadge, PeriodPicker, Segmented } from '../components/PeriodPicker';
import { EmptyState, ErrorState, Loading, describeError } from '../components/States';
import { TZ, defaultInterval, formatDateTime, formatNumber, periodRange, type PeriodKey } from '../lib/format';
import { LIVE_MS } from './DashboardPage';

export const OUTCOME_LABELS: Record<Outcome, string> = {
  redirected: 'Redirected (302)',
  expired: 'Expired (410)',
  cap_reached: 'Click cap reached (410)',
  blocked_admin: 'Disabled by admin (451)',
  blocked_domain: 'Blocked domain (451)',
  quarantined: 'Quarantined (451)',
  inactive: 'Paused or archived (410)',
  not_started: 'Not started yet (404)',
  password_required: 'Password form shown',
};

export function LinkDetailsPage() {
  const id = Number(useParams().id);
  const link = useQuery({ queryKey: ['links', 'one', id], queryFn: () => api.links.get(id), select: (r) => r.link, enabled: Number.isInteger(id) });

  if (!Number.isInteger(id)) return <EmptyState title="This link doesn’t exist." />;
  if (link.isPending) return <Loading label="Loading link" />;
  if (link.isError) {
    // Another user's link is a 404 too, by design: the API never confirms it exists.
    if (link.error instanceof ApiError && link.error.status === 404) {
      return (
        <EmptyState title="This link doesn’t exist, or isn’t yours.">
          <RouterLink to="/links">Back to links</RouterLink>
        </EmptyState>
      );
    }
    return <ErrorState error={link.error} onRetry={() => link.refetch()} />;
  }
  return <Details link={link.data} />;
}

function Details({ link }: { link: Link }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const action = useMutation({
    mutationFn: (kind: 'pause' | 'resume' | 'archive') => api.links[kind](link.id),
    onSuccess: ({ link: updated }) => {
      qc.setQueryData(['links', 'one', link.id], { link: updated });
      qc.invalidateQueries({ queryKey: ['links', 'list'] });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.links.remove(link.id),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ['links', 'one', link.id] });
      qc.invalidateQueries({ queryKey: ['links', 'list'] });
      navigate('/links', { replace: true });
    },
  });

  const devices = link.targets.map((t) => `${t.device} → ${t.destination}`).join(', ');
  const adminDisabled = link.status === 'disabled_admin';

  return (
    <section aria-labelledby="link-title">
      <p className="crumbs">
        <RouterLink to="/links">← Links</RouterLink>
      </p>
      <div className="page-head">
        <div>
          <h1 id="link-title">{link.title || `/${link.code}`}</h1>
          <p className="short-url">
            <a href={link.short_url} target="_blank" rel="noreferrer">
              {link.short_url}
            </a>
            <CopyButton text={link.short_url} />
          </p>
          <span className="badges">
            <StatusBadge link={link} />
            <TrustBadge state={link.trust_state} />
          </span>
        </div>
        <div className="actions">
          {link.status === 'active' && (
            <button type="button" className="btn btn-secondary" disabled={action.isPending} onClick={() => action.mutate('pause')}>
              Pause
            </button>
          )}
          {(link.status === 'paused' || link.status === 'archived') && (
            <button type="button" className="btn btn-secondary" disabled={action.isPending} onClick={() => action.mutate('resume')}>
              Resume
            </button>
          )}
          {(link.status === 'active' || link.status === 'paused') && (
            <button type="button" className="btn btn-secondary" disabled={action.isPending} onClick={() => action.mutate('archive')}>
              Archive
            </button>
          )}
          {!confirmDelete ? (
            <button type="button" className="btn btn-secondary" onClick={() => setConfirmDelete(true)}>
              Delete
            </button>
          ) : (
            <span className="confirm" role="group" aria-label="Confirm delete">
              <span>Delete this link? Its code is never reused and its analytics are kept.</span>
              <button type="button" className="btn btn-danger btn-small" disabled={remove.isPending} onClick={() => remove.mutate()}>
                Yes, delete
              </button>
              <button type="button" className="btn btn-secondary btn-small" onClick={() => setConfirmDelete(false)}>
                Cancel
              </button>
            </span>
          )}
        </div>
      </div>
      {adminDisabled && (
        <p className="form-error" role="note">
          An admin disabled this link. Visitors get 451 until an admin enables it again.
        </p>
      )}
      {(action.isError || remove.isError) && (
        <p className="form-error" role="alert">
          {describeError(action.error ?? remove.error)}
        </p>
      )}

      <div className="grid-2">
        <section className="card" aria-labelledby="facts-title">
          <h2 id="facts-title">Details</h2>
          <dl className="facts facts-compact">
            <Fact label="Destination" value={link.destination} />
            <Fact label="Created" value={formatDateTime(link.created_at)} />
            <Fact label="Starts" value={link.starts_at ? formatDateTime(link.starts_at) : 'Immediately'} />
            <Fact label="Expires" value={link.expires_at ? `${formatDateTime(link.expires_at)}${link.is_expired ? ' (expired)' : ''}` : 'Never'} />
            <Fact label="Click cap" value={link.max_clicks ? `${formatNumber(link.cap_used)} of ${formatNumber(link.max_clicks)} human clicks used` : 'No limit'} />
            <Fact label="Password" value={link.has_password ? 'Required' : 'None'} />
            <Fact label="Device targets" value={devices || 'None'} />
          </dl>
        </section>
        <section className="card" aria-labelledby="qr-title">
          <h2 id="qr-title">QR code</h2>
          <div className="qr">
            <img src={api.links.qrUrl(link.id, 'svg')} alt={`QR code for ${link.short_url}`} width={176} height={176} />
            <div className="actions">
              <a className="btn btn-secondary btn-small" href={api.links.qrUrl(link.id, 'png')} download={`${link.code}-qr.png`}>
                Download PNG
              </a>
              <a className="btn btn-secondary btn-small" href={api.links.qrUrl(link.id, 'svg')} download={`${link.code}-qr.svg`}>
                Download SVG
              </a>
            </div>
            <p className="field-hint">
              Scans open /{link.code}?s=qr, so they are counted apart from clicks. Public transparency page: <a href={`/p/${link.code}`}>/p/{link.code}</a>
            </p>
          </div>
        </section>
      </div>

      <LinkAnalytics linkId={link.id} />
      <TrustPanel linkId={link.id} />
    </section>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

function LinkAnalytics({ linkId }: { linkId: number }) {
  const [period, setPeriod] = useState<PeriodKey>('7d');
  const [interval, setBucket] = useState<'hour' | 'day'>(defaultInterval('7d'));
  const stats = useQuery({
    queryKey: ['analytics', 'link', linkId, period, interval],
    queryFn: () => api.analytics.link(linkId, { ...periodRange(period), tz: TZ, interval }),
    refetchInterval: LIVE_MS,
    placeholderData: keepPreviousData,
  });
  const pickPeriod = (p: PeriodKey) => {
    setPeriod(p);
    setBucket(defaultInterval(p));
  };

  return (
    <section className="card" aria-labelledby="stats-title">
      <div className="page-head">
        <h2 id="stats-title">Analytics</h2>
        <LiveBadge updatedAt={stats.dataUpdatedAt} fetching={stats.isFetching} />
      </div>
      <div className="toolbar">
        <PeriodPicker value={period} onChange={pickPeriod} />
        <Segmented
          label="Interval"
          value={interval}
          options={[
            { value: 'hour', label: 'By hour' },
            { value: 'day', label: 'By day' },
          ]}
          onChange={setBucket}
        />
      </div>
      {stats.isPending ? (
        <Loading label="Loading analytics" />
      ) : stats.isError && !stats.data ? (
        <ErrorState error={stats.error} onRetry={() => stats.refetch()} />
      ) : (
        <>
          <dl className="kpis" aria-label="Visits by traffic class">
            {CLASS_ROWS.map((r) => (
              <Kpi key={r.key} label={r.label} value={formatNumber(stats.data.totals[r.key])} />
            ))}
            <Kpi label="Unique visitor-days" value={formatNumber(stats.data.totals.unique_visitor_days)} hint="hash rotates daily" />
          </dl>
          <SeriesChart series={stats.data.series} interval={stats.data.interval ?? interval} />
          <p className="field-hint">Breakdowns below count human clicks only; bots are in the totals above.</p>
          <div className="breakdowns">
            <BarList title="Source" items={Object.entries(stats.data.by_source ?? {}).map(([k, v]) => ({ label: k === 'qr' ? 'QR scan' : k === 'link' ? 'Link click' : k, value: v }))} />
            <BarList title="Device" items={toItems(stats.data.by_device)} />
            <BarList title="Browser" items={toItems(stats.data.by_browser)} />
            <BarList title="Operating system" items={toItems(stats.data.by_os)} />
            <BarList title="Referrer" items={(stats.data.by_referrer ?? []).map((r) => ({ label: r.host || 'Direct / none', value: r.clicks }))} />
            <BarList title="Country" items={(stats.data.by_country ?? []).map((c) => ({ label: countryName(c.country), value: c.clicks }))} empty="No countries yet (needs GeoLite2)." />
          </div>
          <h3>Outcomes</h3>
          <div className="table-wrap">
            <table className="table">
              <caption className="sr-only">Every visit by what the rule engine did</caption>
              <thead>
                <tr>
                  <th scope="col">Outcome</th>
                  <th scope="col" className="num">
                    Visits
                  </th>
                </tr>
              </thead>
              <tbody>
                {(Object.keys(OUTCOME_LABELS) as Outcome[]).map((o) => (
                  <tr key={o}>
                    <td>{OUTCOME_LABELS[o]}</td>
                    <td className="num">{formatNumber(stats.data.outcomes?.[o] ?? 0)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}

function toItems(rec: Record<string, number> | undefined) {
  return Object.entries(rec ?? {}).map(([label, value]) => ({ label, value }));
}

const regionNames = typeof Intl.DisplayNames === 'function' ? new Intl.DisplayNames(['en'], { type: 'region' }) : null;
function countryName(code: string): string {
  try {
    return regionNames?.of(code) ? `${regionNames.of(code)} (${code})` : code;
  } catch {
    return code || 'Unknown';
  }
}

function verdictTone(v: string): string {
  if (v === 'ok') return 'ok';
  return /quarant|threat|malicious|block/i.test(v) ? 'danger' : 'warn';
}

function TrustPanel({ linkId }: { linkId: number }) {
  const qc = useQueryClient();
  // After a re-scan the worker needs a few seconds, so poll briefly instead of asking the user to reload.
  const [pollUntil, setPollUntil] = useState(0);
  const trust = useQuery({
    queryKey: ['links', 'trust', linkId],
    queryFn: () => api.links.trust(linkId),
    refetchInterval: () => (Date.now() < pollUntil ? 3_000 : false),
  });
  const rescan = useMutation({
    mutationFn: () => api.links.rescan(linkId),
    onSuccess: () => {
      setPollUntil(Date.now() + 60_000);
      qc.invalidateQueries({ queryKey: ['links', 'trust', linkId] });
      qc.invalidateQueries({ queryKey: ['links', 'one', linkId] });
    },
  });

  return (
    <section className="card" aria-labelledby="trust-title">
      <div className="page-head">
        <div>
          <h2 id="trust-title">Destination trust</h2>
          <p className="muted">We keep re-checking where this link goes after it is published.</p>
        </div>
        <button type="button" className="btn btn-secondary" disabled={rescan.isPending} onClick={() => rescan.mutate()}>
          {rescan.isPending ? 'Queuing…' : 'Re-scan now'}
        </button>
      </div>
      <div role="status" aria-live="polite">
        {rescan.isSuccess && <p className="check-ok">Re-scan queued. New results appear here within a minute.</p>}
      </div>
      {rescan.isError && (
        <p className="form-error" role="alert">
          {describeError(rescan.error)}
        </p>
      )}
      {trust.isPending ? (
        <Loading label="Loading trust checks" />
      ) : trust.isError ? (
        <ErrorState error={trust.error} onRetry={() => trust.refetch()} />
      ) : (
        <>
          <dl className="facts facts-compact">
            <div>
              <dt>State</dt>
              <dd>
                <TrustBadge state={trust.data.state} /> <span className="muted">{TRUST[trust.data.state]?.help}</span>
              </dd>
            </div>
            <Fact label="Last check" value={formatDateTime(trust.data.last_checked_at)} />
            <Fact label="Next check" value={formatDateTime(trust.data.next_check_at)} />
          </dl>
          <h3>Check history</h3>
          {trust.data.checks.length === 0 ? (
            <p className="muted">No checks yet. The scanner picks new links up shortly after they are published.</p>
          ) : (
            <ol className="timeline">
              {trust.data.checks.map((c, i) => (
                <CheckItem key={`${c.checked_at}-${i}`} check={c} />
              ))}
            </ol>
          )}
        </>
      )}
    </section>
  );
}

function CheckItem({ check }: { check: TrustCheck }) {
  return (
    <li>
      <div className="timeline-head">
        <span className={`badge badge-${verdictTone(check.verdict)}`}>{check.verdict}</span>
        <time dateTime={check.checked_at}>{formatDateTime(check.checked_at)}</time>
      </div>
      <p className="timeline-body">
        HTTP {check.http_status || '—'} · {check.hops} redirect{check.hops === 1 ? '' : 's'} · ends at <span className="mono">{check.final_domain || '—'}</span>
        {check.sources?.length > 0 && <> · checked with {check.sources.join(', ')}</>}
      </p>
      {check.final_url && (
        <p className="timeline-body muted truncate" title={check.final_url}>
          {check.final_url}
        </p>
      )}
    </li>
  );
}
