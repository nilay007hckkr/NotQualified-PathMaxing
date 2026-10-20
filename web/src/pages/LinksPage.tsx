import { keepPreviousData, useInfiniteQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { Link as RouterLink } from 'react-router';
import { api } from '../api/client';
import { useCampaigns } from '../api/hooks';
import type { Link, LinkFilters, LinkStatus, TrustState } from '../api/types';
import { STATUS_OPTIONS, StatusBadge, TRUST_OPTIONS, TrustBadge } from '../components/Badges';
import { CopyButton } from '../components/CopyButton';
import { Field, SelectField } from '../components/Field';
import { EmptyState, ErrorState, Loading } from '../components/States';
import { formatDateTime, formatNumber } from '../lib/format';

const PAGE_SIZE = 25;

/** Waits until the user stops typing, so a search isn't sent on every keystroke. */
function useDebounced<T>(value: T, ms = 300): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

export function LinksPage() {
  const [q, setQ] = useState('');
  const [status, setStatus] = useState<LinkStatus | ''>('');
  const [trust, setTrust] = useState<TrustState | ''>('');
  const [campaign, setCampaign] = useState('');
  const campaigns = useCampaigns();
  const search = useDebounced(q.trim());

  const filters: LinkFilters = {
    q: search || undefined,
    status: status || undefined,
    trust_state: trust || undefined,
    campaign_id: campaign ? Number(campaign) : undefined,
    limit: PAGE_SIZE,
  };
  const links = useInfiniteQuery({
    queryKey: ['links', 'list', filters],
    queryFn: ({ pageParam }) => api.links.list({ ...filters, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    placeholderData: keepPreviousData,
  });
  const rows = links.data?.pages.flatMap((p) => p.items) ?? [];
  const filtered = Boolean(search || status || trust || campaign);

  return (
    <section aria-labelledby="links-title">
      <div className="page-head">
        <div>
          <p className="kicker">Your links</p>
          <h1 id="links-title">Links</h1>
          <p className="muted">Every link keeps being re-checked after you publish it.</p>
        </div>
        <RouterLink to="/links/new" className="btn btn-primary">
          Create link
        </RouterLink>
      </div>

      <form className="toolbar" role="search" aria-label="Filter links" onSubmit={(e) => e.preventDefault()}>
        <Field label="Search" type="search" placeholder="Alias, title or destination" value={q} onChange={(e) => setQ(e.target.value)} />
        <SelectField label="Status" value={status} placeholder="Any status" options={STATUS_OPTIONS} onChange={(e) => setStatus(e.target.value as LinkStatus | '')} />
        <SelectField label="Trust" value={trust} placeholder="Any trust state" options={TRUST_OPTIONS} onChange={(e) => setTrust(e.target.value as TrustState | '')} />
        {campaigns.isSuccess && campaigns.data.length > 0 && (
          <SelectField
            label="Campaign"
            value={campaign}
            placeholder="Any campaign"
            options={campaigns.data.map((c) => ({ value: c.id, label: c.name }))}
            onChange={(e) => setCampaign(e.target.value)}
          />
        )}
      </form>

      {links.isPending ? (
        <Loading label="Loading links" />
      ) : links.isError && rows.length === 0 ? (
        <ErrorState error={links.error} onRetry={() => links.refetch()} />
      ) : rows.length === 0 ? (
        <EmptyState title={filtered ? 'No links match these filters.' : 'You have no links yet.'}>
          {!filtered && (
            <RouterLink to="/links/new" className="btn btn-primary">
              Create your first link
            </RouterLink>
          )}
        </EmptyState>
      ) : (
        <div className="card">
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Short link</th>
                  <th scope="col">Destination</th>
                  <th scope="col">Status</th>
                  <th scope="col">Trust</th>
                  <th scope="col" className="num">
                    Cap used
                  </th>
                  <th scope="col">Created</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((l) => (
                  <LinkRow key={l.id} link={l} />
                ))}
              </tbody>
            </table>
          </div>
          {links.hasNextPage && (
            <div className="list-more">
              <button type="button" className="btn btn-secondary" disabled={links.isFetchingNextPage} onClick={() => links.fetchNextPage()}>
                {links.isFetchingNextPage ? 'Loading…' : 'Load more'}
              </button>
            </div>
          )}
        </div>
      )}
    </section>
  );
}

function LinkRow({ link }: { link: Link }) {
  return (
    <tr>
      <td>
        <div className="cell-stack">
          <RouterLink to={`/links/${link.id}`} className="mono">
            /{link.code}
          </RouterLink>
          <CopyButton text={link.short_url} />
        </div>
      </td>
      <td>
        {link.title && <div className="cell-title">{link.title}</div>}
        <span className="truncate muted" title={link.destination}>
          {link.destination}
        </span>
      </td>
      <td>
        <StatusBadge link={link} />
      </td>
      <td>
        <TrustBadge state={link.trust_state} />
      </td>
      <td className="num">{link.max_clicks ? `${formatNumber(link.cap_used)} / ${formatNumber(link.max_clicks)}` : '—'}</td>
      <td>{formatDateTime(link.created_at)}</td>
    </tr>
  );
}
