import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { Link as RouterLink } from 'react-router';
import { api } from '../api/client';
import type { AlertKind } from '../api/types';
import { OverviewBody } from '../components/Overview';
import { LiveBadge, PeriodPicker } from '../components/PeriodPicker';
import { EmptyState, ErrorState, Loading } from '../components/States';
import { TZ, formatDateTime, periodRange, type PeriodKey } from '../lib/format';

// Polling every 5 s is what makes the dashboard "live" during the demo; the
// API also offers SSE, but polling survives proxies and sleeping laptops.
export const LIVE_MS = 5_000;

export const ALERT_LABELS: Record<AlertKind, string> = {
  spike: 'Traffic spike',
  near_cap: 'Near its click cap',
  expiring: 'Expiring soon',
  dead_link: 'Destination is down',
  quarantined: 'Quarantined',
};

export function DashboardPage() {
  const [period, setPeriod] = useState<PeriodKey>('7d');
  const overview = useQuery({
    queryKey: ['analytics', 'overview', period],
    queryFn: () => api.analytics.overview({ ...periodRange(period), tz: TZ }),
    refetchInterval: LIVE_MS,
    placeholderData: keepPreviousData,
  });

  return (
    <section aria-labelledby="dash-title">
      <div className="page-head">
        <div>
          <p className="kicker">Live dashboard</p>
          <h1 id="dash-title">Overview</h1>
          <p className="muted">Every visit to your links, labelled honestly: humans, bots, and visits a rule refused.</p>
        </div>
        <div className="actions">
          <LiveBadge updatedAt={overview.dataUpdatedAt} fetching={overview.isFetching} />
          <RouterLink to="/links/new" className="btn btn-primary">
            Create link
          </RouterLink>
        </div>
      </div>
      <div className="toolbar">
        <PeriodPicker value={period} onChange={setPeriod} />
      </div>

      {overview.isPending ? (
        <Loading label="Loading analytics" />
      ) : overview.isError && !overview.data ? (
        <ErrorState error={overview.error} onRetry={() => overview.refetch()} />
      ) : (
        <OverviewBody data={overview.data} linkTo={(id, code) => <RouterLink to={`/links/${id}`}>/{code}</RouterLink>} />
      )}

      <OpenAlerts />
    </section>
  );
}

function OpenAlerts() {
  const alerts = useQuery({
    queryKey: ['alerts', 'open'],
    queryFn: () => api.alerts.list('open'),
    refetchInterval: LIVE_MS,
  });
  return (
    <section className="card" aria-labelledby="alerts-title">
      <h2 id="alerts-title">Open alerts</h2>
      {alerts.isPending ? (
        <Loading label="Loading alerts" />
      ) : alerts.isError ? (
        <ErrorState error={alerts.error} onRetry={() => alerts.refetch()} />
      ) : alerts.data.items.length === 0 ? (
        <EmptyState title="No open alerts." />
      ) : (
        <ul className="alert-list">
          {alerts.data.items.map((a) => (
            <li key={a.id} className={`alert-item alert-${a.kind}`}>
              <span className="badge badge-warn">{ALERT_LABELS[a.kind] ?? a.kind}</span>
              <RouterLink to={`/links/${a.link_id}`}>{a.message}</RouterLink>
              <time className="muted" dateTime={a.created_at}>
                {formatDateTime(a.created_at)}
              </time>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
