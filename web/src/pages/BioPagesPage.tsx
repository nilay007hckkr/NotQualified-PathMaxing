import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { ApiError, api } from '../api/client';
import type { BioPage } from '../api/types';
import { Field } from '../components/Field';
import { EmptyState, ErrorState, Loading, describeError } from '../components/States';
import './bulk.css';

const bioKey = ['bio-pages'] as const;
const SLUG = /^[a-z0-9][a-z0-9_-]{1,48}[a-z0-9]$/i;

type Draft = { id?: number; slug: string; title: string; link_ids: number[] };
const EMPTY: Draft = { slug: '', title: '', link_ids: [] };

export function BioPagesPage() {
  const qc = useQueryClient();
  const pages = useQuery({ queryKey: bioKey, queryFn: () => api.bioPages.list(), select: (p) => p.items });
  // Up to 100 links is plenty for a bio page picker in this prototype.
  const links = useQuery({ queryKey: ['links', 'list', 'bio-picker'], queryFn: () => api.links.list({ limit: 100, status: 'active' }), select: (p) => p.items });
  const [draft, setDraft] = useState<Draft>(EMPTY);
  const [touched, setTouched] = useState(false);

  const save = useMutation({
    mutationFn: (d: Draft) => {
      const body = { slug: d.slug.trim(), title: d.title.trim(), link_ids: d.link_ids };
      return d.id ? api.bioPages.update(d.id, body) : api.bioPages.create(body);
    },
    onSuccess: () => {
      setDraft(EMPTY);
      setTouched(false);
      qc.invalidateQueries({ queryKey: bioKey });
    },
  });

  const slugError = touched && !SLUG.test(draft.slug.trim()) ? 'Use 3–50 letters, digits, - or _.' : save.error instanceof ApiError && save.error.status === 409 ? 'This address is already taken.' : undefined;
  const titleError = touched && !draft.title.trim() ? 'Give the page a title.' : undefined;
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (SLUG.test(draft.slug.trim()) && draft.title.trim()) save.mutate(draft);
  };
  const editPage = (p: BioPage) => {
    save.reset();
    setTouched(false);
    setDraft({ id: p.id, slug: p.slug, title: p.title, link_ids: p.link_ids ?? [] });
  };
  const toggle = (id: number, on: boolean) => setDraft((d) => ({ ...d, link_ids: on ? [...d.link_ids, id] : d.link_ids.filter((x) => x !== id) }));

  return (
    <section aria-labelledby="bio-title">
      <div className="page-head">
        <div>
          <p className="kicker">Link-in-bio</p>
          <h1 id="bio-title">Link-in-bio pages</h1>
          <p className="muted">One public page that lists your chosen links, at /bio/your-name.</p>
        </div>
      </div>

      <div className="grid-2">
        <form className="card" onSubmit={onSubmit} noValidate aria-labelledby="bio-form-title">
          <h2 id="bio-form-title">{draft.id ? `Edit /bio/${draft.slug}` : 'New bio page'}</h2>
          <Field label="Address" value={draft.slug} hint={`Public at ${location.origin}/bio/${draft.slug.trim() || 'your-name'}`} error={slugError} onChange={(e) => setDraft({ ...draft, slug: e.target.value })} />
          <Field label="Page title" value={draft.title} error={titleError} onChange={(e) => setDraft({ ...draft, title: e.target.value })} />
          <fieldset className="fieldset">
            <legend>Links on the page</legend>
            {links.isPending ? (
              <Loading label="Loading links" />
            ) : links.isError ? (
              <ErrorState error={links.error} onRetry={() => links.refetch()} />
            ) : links.data.length === 0 ? (
              <p className="muted">You have no active links yet.</p>
            ) : (
              <div className="checks checks-column">
                {links.data.map((l) => (
                  <label key={l.id} className="check">
                    <input type="checkbox" checked={draft.link_ids.includes(l.id)} onChange={(e) => toggle(l.id, e.target.checked)} />
                    <span>
                      <span className="mono">/{l.code}</span> {l.title && <span className="muted">· {l.title}</span>}
                    </span>
                  </label>
                ))}
              </div>
            )}
          </fieldset>
          {save.isError && !slugError && (
            <p className="form-error" role="alert">
              {describeError(save.error)}
            </p>
          )}
          <div className="actions">
            <button type="submit" className="btn btn-primary" disabled={save.isPending}>
              {save.isPending ? 'Saving…' : draft.id ? 'Save changes' : 'Create page'}
            </button>
            {draft.id && (
              <button type="button" className="btn btn-secondary" onClick={() => setDraft(EMPTY)}>
                Cancel
              </button>
            )}
          </div>
        </form>

        <section className="card" aria-labelledby="bio-list-title">
          <h2 id="bio-list-title">Your pages</h2>
          {pages.isPending ? (
            <Loading label="Loading pages" />
          ) : pages.isError ? (
            <ErrorState error={pages.error} onRetry={() => pages.refetch()} />
          ) : pages.data.length === 0 ? (
            <EmptyState title="No bio pages yet." />
          ) : (
            <ul className="plain-list">
              {pages.data.map((p) => (
                <li key={p.id}>
                  <div>
                    <strong>{p.title}</strong>
                    <div className="muted">
                      {/* A plain anchor: /bio/ is served by the Go server, outside the /app router. */}
                      <a href={`/bio/${p.slug}`} target="_blank" rel="noreferrer">
                        /bio/{p.slug}
                      </a>{' '}
                      · {p.link_ids?.length ?? 0} links
                    </div>
                  </div>
                  <button type="button" className="btn btn-secondary btn-small" onClick={() => editPage(p)} aria-label={`Edit ${p.title}`}>
                    Edit
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </section>
  );
}
