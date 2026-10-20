import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { Link as RouterLink, useNavigate, useParams } from 'react-router';
import { ApiError, api } from '../api/client';
import { campaignsKey, tagsKey, useCampaigns, useTags } from '../api/hooks';
import type { Campaign } from '../api/types';
import { CLASS_ROWS, SeriesChart } from '../components/charts';
import { Field } from '../components/Field';
import { Kpi } from '../components/Overview';
import { LiveBadge, PeriodPicker } from '../components/PeriodPicker';
import { EmptyState, ErrorState, Loading, describeError } from '../components/States';
import { TZ, defaultInterval, formatDateTime, formatNumber, periodRange, type PeriodKey } from '../lib/format';
import './bulk.css';
import { LIVE_MS } from './DashboardPage';

type CampaignForm = Pick<Campaign, 'name' | 'utm_source' | 'utm_medium' | 'utm_campaign'>;
const EMPTY: CampaignForm = { name: '', utm_source: '', utm_medium: '', utm_campaign: '' };

function CampaignFields({ value, onChange, nameError }: { value: CampaignForm; onChange: (v: CampaignForm) => void; nameError?: string }) {
  return (
    <>
      <Field label="Name" required value={value.name} error={nameError} onChange={(e) => onChange({ ...value, name: e.target.value })} />
      <div className="grid-3">
        <Field label="utm_source" value={value.utm_source} hint="e.g. instagram" onChange={(e) => onChange({ ...value, utm_source: e.target.value })} />
        <Field label="utm_medium" value={value.utm_medium} hint="e.g. social" onChange={(e) => onChange({ ...value, utm_medium: e.target.value })} />
        <Field label="utm_campaign" value={value.utm_campaign} hint="e.g. diwali-2026" onChange={(e) => onChange({ ...value, utm_campaign: e.target.value })} />
      </div>
    </>
  );
}

export function CampaignsPage() {
  const qc = useQueryClient();
  const campaigns = useCampaigns();
  const [form, setForm] = useState<CampaignForm>(EMPTY);
  const [touched, setTouched] = useState(false);
  const create = useMutation({
    mutationFn: () => api.campaigns.create({ ...form, name: form.name.trim() }),
    onSuccess: () => {
      setForm(EMPTY);
      setTouched(false);
      qc.invalidateQueries({ queryKey: campaignsKey });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (form.name.trim()) create.mutate();
  };

  return (
    <section aria-labelledby="campaigns-title">
      <div className="page-head">
        <div>
          <p className="kicker">Campaigns and tags</p>
          <h1 id="campaigns-title">Campaigns</h1>
          <p className="muted">Group links, add UTM parameters, and compare human clicks per campaign.</p>
        </div>
      </div>

      <div className="grid-2">
        <form className="card" onSubmit={onSubmit} noValidate aria-labelledby="new-campaign">
          <h2 id="new-campaign">New campaign</h2>
          <CampaignFields value={form} onChange={setForm} nameError={touched && !form.name.trim() ? 'Give the campaign a name.' : undefined} />
          {create.isError && (
            <p className="form-error" role="alert">
              {create.error instanceof ApiError && create.error.status === 409 ? 'A campaign with this name already exists.' : describeError(create.error)}
            </p>
          )}
          <button type="submit" className="btn btn-primary" disabled={create.isPending}>
            {create.isPending ? 'Creating…' : 'Create campaign'}
          </button>
        </form>

        <section className="card" aria-labelledby="campaign-list">
          <h2 id="campaign-list">Your campaigns</h2>
          {campaigns.isPending ? (
            <Loading label="Loading campaigns" />
          ) : campaigns.isError ? (
            <ErrorState error={campaigns.error} onRetry={() => campaigns.refetch()} />
          ) : campaigns.data.length === 0 ? (
            <EmptyState title="No campaigns yet." />
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Name</th>
                    <th scope="col">UTM</th>
                    <th scope="col" className="num">
                      Links
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {campaigns.data.map((c) => (
                    <tr key={c.id}>
                      <td>
                        <RouterLink to={`/campaigns/${c.id}`}>{c.name}</RouterLink>
                      </td>
                      <td className="muted">{[c.utm_source, c.utm_medium, c.utm_campaign].filter(Boolean).join(' · ') || '—'}</td>
                      <td className="num">{formatNumber(c.link_count)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>

      <TagsCard />
    </section>
  );
}

function TagsCard() {
  const qc = useQueryClient();
  const tags = useTags();
  const [name, setName] = useState('');
  const [color, setColor] = useState('#0f766e');
  const create = useMutation({
    mutationFn: () => api.tags.create({ name: name.trim(), color }),
    onSuccess: () => {
      setName('');
      qc.invalidateQueries({ queryKey: tagsKey });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.tags.remove(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: tagsKey }),
  });

  return (
    <section className="card" aria-labelledby="tags-title">
      <h2 id="tags-title">Tags</h2>
      <form
        className="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) create.mutate();
        }}
      >
        <Field label="Tag name" value={name} onChange={(e) => setName(e.target.value)} />
        <Field label="Colour" type="color" className="field-color" value={color} onChange={(e) => setColor(e.target.value)} />
        <button type="submit" className="btn btn-primary" disabled={create.isPending || !name.trim()}>
          Add tag
        </button>
      </form>
      {(create.isError || remove.isError) && (
        <p className="form-error" role="alert">
          {describeError(create.error ?? remove.error)}
        </p>
      )}
      {tags.isPending ? (
        <Loading label="Loading tags" />
      ) : tags.isError ? (
        <ErrorState error={tags.error} onRetry={() => tags.refetch()} />
      ) : tags.data.length === 0 ? (
        <p className="muted">No tags yet.</p>
      ) : (
        <ul className="chips">
          {tags.data.map((t) => (
            <li key={t.id} className="chip">
              <span className="chip-dot" style={{ background: t.color || 'var(--text-muted)' }} aria-hidden="true" />
              {t.name}
              <button type="button" className="chip-remove" aria-label={`Delete tag ${t.name}`} disabled={remove.isPending} onClick={() => remove.mutate(t.id)}>
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export function CampaignDetailsPage() {
  const id = Number(useParams().id);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const campaign = useQuery({ queryKey: ['campaigns', 'one', id], queryFn: () => api.campaigns.get(id), select: (r) => r.campaign });
  const [edit, setEdit] = useState<CampaignForm | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const save = useMutation({
    mutationFn: (v: CampaignForm) => api.campaigns.update(id, { ...v, name: v.name.trim() }),
    onSuccess: (r) => {
      qc.setQueryData(['campaigns', 'one', id], r);
      qc.invalidateQueries({ queryKey: campaignsKey, exact: true });
      setEdit(null);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.campaigns.remove(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: campaignsKey, exact: true });
      navigate('/campaigns', { replace: true });
    },
  });

  if (campaign.isPending) return <Loading label="Loading campaign" />;
  if (campaign.isError) {
    if (campaign.error instanceof ApiError && campaign.error.status === 404) return <EmptyState title="This campaign doesn’t exist, or isn’t yours." />;
    return <ErrorState error={campaign.error} onRetry={() => campaign.refetch()} />;
  }
  const c = campaign.data;

  return (
    <section aria-labelledby="campaign-title">
      <p className="crumbs">
        <RouterLink to="/campaigns">← Campaigns</RouterLink>
      </p>
      <div className="page-head">
        <div>
          <p className="kicker">Campaign</p>
          <h1 id="campaign-title">{c.name}</h1>
          <p className="muted">
            {formatNumber(c.link_count)} links · created {formatDateTime(c.created_at)}
          </p>
        </div>
        <div className="actions">
          {!edit && (
            <button type="button" className="btn btn-secondary" onClick={() => setEdit({ name: c.name, utm_source: c.utm_source, utm_medium: c.utm_medium, utm_campaign: c.utm_campaign })}>
              Edit
            </button>
          )}
          {!confirmDelete ? (
            <button type="button" className="btn btn-secondary" onClick={() => setConfirmDelete(true)}>
              Delete
            </button>
          ) : (
            <span className="confirm" role="group" aria-label="Confirm delete">
              <span>Delete this campaign? Its links stay.</span>
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
      {remove.isError && (
        <p className="form-error" role="alert">
          {describeError(remove.error)}
        </p>
      )}

      {edit && (
        <form
          className="card"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            if (edit.name.trim()) save.mutate(edit);
          }}
          aria-labelledby="edit-campaign"
        >
          <h2 id="edit-campaign">Edit campaign</h2>
          <CampaignFields value={edit} onChange={setEdit} nameError={!edit.name.trim() ? 'Give the campaign a name.' : undefined} />
          {save.isError && (
            <p className="form-error" role="alert">
              {describeError(save.error)}
            </p>
          )}
          <div className="actions">
            <button type="submit" className="btn btn-primary" disabled={save.isPending}>
              Save
            </button>
            <button type="button" className="btn btn-secondary" onClick={() => setEdit(null)}>
              Cancel
            </button>
          </div>
        </form>
      )}

      <CampaignAnalytics id={id} />
    </section>
  );
}

function CampaignAnalytics({ id }: { id: number }) {
  const [period, setPeriod] = useState<PeriodKey>('7d');
  const stats = useQuery({
    queryKey: ['analytics', 'campaign', id, period],
    queryFn: () => api.analytics.campaign(id, { ...periodRange(period), tz: TZ, interval: defaultInterval(period) }),
    refetchInterval: LIVE_MS,
    placeholderData: keepPreviousData,
  });
  return (
    <section className="card" aria-labelledby="campaign-stats">
      <div className="page-head">
        <h2 id="campaign-stats">Analytics</h2>
        <LiveBadge updatedAt={stats.dataUpdatedAt} fetching={stats.isFetching} />
      </div>
      <div className="toolbar">
        <PeriodPicker value={period} onChange={setPeriod} />
      </div>
      {stats.isPending ? (
        <Loading label="Loading analytics" />
      ) : stats.isError && !stats.data ? (
        <ErrorState error={stats.error} onRetry={() => stats.refetch()} />
      ) : (
        <>
          <dl className="kpis" aria-label="Campaign visits by traffic class">
            {CLASS_ROWS.map((r) => (
              <Kpi key={r.key} label={r.label} value={formatNumber(stats.data.totals[r.key])} />
            ))}
            <Kpi label="Unique visitor-days" value={formatNumber(stats.data.totals.unique_visitor_days)} />
          </dl>
          <SeriesChart series={stats.data.series} interval={stats.data.interval ?? defaultInterval(period)} />
          <h3>Top links</h3>
          {stats.data.top_links.length === 0 ? (
            <p className="muted">No human clicks on this campaign’s links in this period.</p>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Link</th>
                    <th scope="col" className="num">
                      Human clicks
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {stats.data.top_links.map((l) => (
                    <tr key={l.link_id}>
                      <td className="mono">
                        <RouterLink to={`/links/${l.link_id}`}>/{l.code}</RouterLink>
                      </td>
                      <td className="num">{formatNumber(l.human)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </section>
  );
}
