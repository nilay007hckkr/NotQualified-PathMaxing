import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link as RouterLink } from 'react-router';
import { ApiError, api } from '../api/client';
import { useCampaigns, useTags } from '../api/hooks';
import type { CreateLinkResult, Device, Issue, LinkInput, ValidationResult } from '../api/types';
import { CopyButton } from '../components/CopyButton';
import { Field, SelectField } from '../components/Field';
import { describeError } from '../components/States';
import { formatDateTime, formatNumber, istLocalToUtc } from '../lib/format';
import { STEP1_FIELDS, explain, fieldOf } from '../lib/issues';

const DEVICES: Device[] = ['mobile', 'tablet', 'desktop'];

interface FormState {
  destination: string;
  alias: string;
  title: string;
  starts_at: string;
  expires_at: string;
  max_clicks: string;
  password: string;
  targets: Record<Device, string>;
  campaign_id: string;
  tag_ids: number[];
}

const EMPTY: FormState = {
  destination: '',
  alias: '',
  title: '',
  starts_at: '',
  expires_at: '',
  max_clicks: '',
  password: '',
  targets: { mobile: '', tablet: '', desktop: '' },
  campaign_id: '',
  tag_ids: [],
};

type Stage = 'destination' | 'options' | 'review' | 'done';

/** Step 1 sends only the destination and alias; later steps send everything. */
export function toInput(f: FormState, full: boolean): LinkInput {
  const input: LinkInput = { destination: f.destination.trim() };
  if (f.alias.trim()) input.alias = f.alias.trim();
  if (!full) return input;
  if (f.title.trim()) input.title = f.title.trim();
  // datetime-local has no zone; the form is labelled IST and the API wants UTC.
  input.starts_at = istLocalToUtc(f.starts_at);
  input.expires_at = istLocalToUtc(f.expires_at);
  input.max_clicks = f.max_clicks.trim() ? Number(f.max_clicks) : null;
  if (f.password) input.password = f.password;
  const targets = DEVICES.filter((d) => f.targets[d].trim()).map((device) => ({ device, destination: f.targets[device].trim() }));
  if (targets.length) input.targets = targets;
  input.campaign_id = f.campaign_id ? Number(f.campaign_id) : null;
  if (f.tag_ids.length) input.tag_ids = f.tag_ids;
  return input;
}

const STEPS: { stage: Stage; label: string }[] = [
  { stage: 'destination', label: 'Destination' },
  { stage: 'options', label: 'Options' },
  { stage: 'review', label: 'Review and publish' },
];

export function NewLinkPage() {
  const qc = useQueryClient();
  const [form, setForm] = useState<FormState>(EMPTY);
  const [stage, setStage] = useState<Stage>('destination');
  // The latest server verdict for the step on screen. Editing a checked
  // field clears it, so "Continue" always reflects the values shown.
  const [result, setResult] = useState<ValidationResult | null>(null);
  const [reviewWarnings, setReviewWarnings] = useState<Issue[]>([]);
  const [created, setCreated] = useState<CreateLinkResult | null>(null);
  const [localError, setLocalError] = useState<Record<string, string>>({});
  const headingRef = useRef<HTMLHeadingElement>(null);

  // Move focus to the new step's heading so keyboard and screen-reader users land on it.
  const shownStage = useRef(stage);
  useEffect(() => {
    if (shownStage.current === stage) return;
    shownStage.current = stage;
    headingRef.current?.focus();
  }, [stage]);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm((f) => ({ ...f, [key]: value }));
    if (key === 'destination' || key === 'alias') setResult(null);
  };

  /** Sends server errors back to the step that owns the field. */
  const routeIssues = (v: ValidationResult) => {
    setResult(v);
    setStage(v.errors.some((e) => STEP1_FIELDS.includes(fieldOf(e))) ? 'destination' : 'options');
  };

  const check = useMutation({
    mutationFn: () => api.links.validate(toInput(form, false)),
    onSuccess: (v) => setResult(v),
  });

  const review = useMutation({
    mutationFn: () => api.links.validate(toInput(form, true)),
    onSuccess: (v) => {
      if (v.errors.length) return routeIssues(v);
      setResult(null);
      setReviewWarnings(v.warnings);
      setStage('review');
    },
  });

  const publish = useMutation({
    mutationFn: () => api.links.create(toInput(form, true)),
    onSuccess: (res) => {
      setCreated(res);
      setStage('done');
      qc.invalidateQueries({ queryKey: ['links'] });
      qc.invalidateQueries({ queryKey: ['analytics'] });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 422 && err.validation) return routeIssues(err.validation);
      if (err instanceof ApiError && err.status === 409) {
        // The alias was free at validation time but someone took it before insert.
        routeIssues({ errors: [{ field: 'alias', code: 'ALIAS_TAKEN', message: err.message }], warnings: [] });
      }
    },
  });

  const errorFor = (field: string) => {
    if (localError[field]) return localError[field];
    const issue = result?.errors.find((e) => fieldOf(e) === field);
    return issue ? explain(issue) : undefined;
  };
  const step1Clean = result !== null && result.errors.length === 0 && stage === 'destination';

  const onCheck = (e: FormEvent) => {
    e.preventDefault();
    if (step1Clean) return setStage('options');
    if (!form.destination.trim()) {
      setLocalError({ destination: 'Enter the address people should land on.' });
      return;
    }
    setLocalError({});
    check.mutate();
  };

  const onReview = (e: FormEvent) => {
    e.preventDefault();
    const cap = form.max_clicks.trim();
    if (cap && !(Number.isInteger(Number(cap)) && Number(cap) >= 1)) {
      setLocalError({ max_clicks: explain({ field: 'max_clicks', code: 'CAP_INVALID', message: '' }) });
      return;
    }
    setLocalError({});
    review.mutate();
  };

  const restart = () => {
    setForm(EMPTY);
    setResult(null);
    setCreated(null);
    setReviewWarnings([]);
    publish.reset();
    setStage('destination');
  };

  const campaigns = useCampaigns();
  const tags = useTags();
  const campaignName = campaigns.data?.find((c) => String(c.id) === form.campaign_id)?.name;
  const otherErrors = result?.errors.filter((e) => !['destination', 'alias', 'title', 'starts_at', 'expires_at', 'max_clicks', 'password', 'targets', 'campaign_id'].includes(fieldOf(e))) ?? [];

  return (
    <section aria-labelledby="wizard-title" className="wizard">
      <div className="page-head">
        <div>
          <p className="kicker">New short link</p>
          <h1 id="wizard-title">Create a link</h1>
          <p className="muted">We check the destination and alias before anything is saved.</p>
        </div>
      </div>

      {stage !== 'done' && (
        <ol className="steps" aria-label="Progress">
          {STEPS.map((s, i) => (
            <li key={s.stage} aria-current={s.stage === stage ? 'step' : undefined} className={s.stage === stage ? 'is-current' : undefined}>
              <span className="step-num">{i + 1}</span> {s.label}
            </li>
          ))}
        </ol>
      )}

      {stage === 'destination' && (
        <form className="card" onSubmit={onCheck} noValidate>
          <h2 ref={headingRef} tabIndex={-1}>
            Step 1: Where should it go?
          </h2>
          <Field
            label="Destination URL"
            type="url"
            name="destination"
            required
            placeholder="https://example.com/page"
            value={form.destination}
            error={errorFor('destination')}
            onChange={(e) => set('destination', e.target.value)}
          />
          <Field
            label="Custom alias (optional)"
            name="alias"
            value={form.alias}
            hint="3–50 letters, digits, - or _. Leave it empty for a random 8-character code. Letter case is ignored."
            error={errorFor('alias')}
            onChange={(e) => set('alias', e.target.value)}
          />
          <IssueList title="Other problems" issues={otherErrors} tone="error" />
          {step1Clean && (
            <div className="check-ok" role="status">
              <p>
                <strong>Looks good.</strong> The destination and alias passed every check.
              </p>
              <IssueList title="Worth knowing" issues={result?.warnings ?? []} tone="warn" />
            </div>
          )}
          {check.isError && (
            <p className="form-error" role="alert">
              {describeError(check.error)}
            </p>
          )}
          <div className="actions">
            <button type="submit" className="btn btn-primary" disabled={check.isPending}>
              {check.isPending ? 'Checking…' : step1Clean ? 'Continue' : 'Check destination'}
            </button>
          </div>
        </form>
      )}

      {stage === 'options' && (
        <form className="card" onSubmit={onReview} noValidate>
          <h2 ref={headingRef} tabIndex={-1}>
            Step 2: Options
          </h2>
          <p className="muted">All optional. Times are India time (IST).</p>
          <Field label="Title" name="title" value={form.title} error={errorFor('title')} onChange={(e) => set('title', e.target.value)} />
          <div className="grid-2 tight">
            <Field label="Starts at (IST)" type="datetime-local" name="starts_at" value={form.starts_at} error={errorFor('starts_at')} hint="Before this, visitors get 404." onChange={(e) => set('starts_at', e.target.value)} />
            <Field label="Expires at (IST)" type="datetime-local" name="expires_at" value={form.expires_at} error={errorFor('expires_at')} hint="From this moment, visitors get 410." onChange={(e) => set('expires_at', e.target.value)} />
            <Field
              label="Click limit"
              type="number"
              name="max_clicks"
              min={1}
              step={1}
              inputMode="numeric"
              value={form.max_clicks}
              error={errorFor('max_clicks')}
              hint="Only human clicks use it up; bots and link previews never do."
              onChange={(e) => set('max_clicks', e.target.value)}
            />
            <Field label="Password" type="password" name="password" autoComplete="new-password" value={form.password} error={errorFor('password')} hint="Visitors must enter it before they are redirected." onChange={(e) => set('password', e.target.value)} />
          </div>

          <fieldset className="fieldset" aria-describedby={errorFor('targets') ? 'targets-error' : 'targets-hint'}>
            <legend>Device targeting</legend>
            <p id="targets-hint" className="field-hint">
              Visitors on these devices go to a different destination. Leave empty to use the main one.
            </p>
            <div className="grid-3">
              {DEVICES.map((d) => (
                <Field
                  key={d}
                  label={`${d[0].toUpperCase()}${d.slice(1)} destination`}
                  type="url"
                  placeholder="https://"
                  value={form.targets[d]}
                  onChange={(e) => set('targets', { ...form.targets, [d]: e.target.value })}
                />
              ))}
            </div>
            {errorFor('targets') && (
              <p id="targets-error" className="field-error">
                {errorFor('targets')}
              </p>
            )}
          </fieldset>

          {campaigns.isSuccess && campaigns.data.length > 0 && (
            <SelectField
              label="Campaign"
              value={form.campaign_id}
              placeholder="No campaign"
              options={campaigns.data.map((c) => ({ value: c.id, label: c.name }))}
              error={errorFor('campaign_id')}
              onChange={(e) => set('campaign_id', e.target.value)}
            />
          )}
          {tags.isSuccess && tags.data.length > 0 && (
            <fieldset className="fieldset">
              <legend>Tags</legend>
              <div className="checks">
                {tags.data.map((t) => (
                  <label key={t.id} className="check">
                    <input
                      type="checkbox"
                      checked={form.tag_ids.includes(t.id)}
                      onChange={(e) => set('tag_ids', e.target.checked ? [...form.tag_ids, t.id] : form.tag_ids.filter((id) => id !== t.id))}
                    />
                    {t.name}
                  </label>
                ))}
              </div>
            </fieldset>
          )}

          <IssueList title="Other problems" issues={otherErrors} tone="error" />
          {review.isError && (
            <p className="form-error" role="alert">
              {describeError(review.error)}
            </p>
          )}
          <div className="actions">
            <button type="button" className="btn btn-secondary" onClick={() => setStage('destination')}>
              Back
            </button>
            <button type="submit" className="btn btn-primary" disabled={review.isPending}>
              {review.isPending ? 'Checking…' : 'Review'}
            </button>
          </div>
        </form>
      )}

      {stage === 'review' && (
        <div className="card">
          <h2 ref={headingRef} tabIndex={-1}>
            Step 3: Review and publish
          </h2>
          <dl className="facts">
            <Fact label="Destination" value={form.destination.trim()} />
            <Fact label="Alias" value={form.alias.trim() || 'Random 8-character code'} />
            <Fact label="Title" value={form.title.trim() || '—'} />
            <Fact label="Starts" value={form.starts_at ? formatDateTime(istLocalToUtc(form.starts_at)) : 'Immediately'} />
            <Fact label="Expires" value={form.expires_at ? formatDateTime(istLocalToUtc(form.expires_at)) : 'Never'} />
            <Fact label="Click limit" value={form.max_clicks ? `${formatNumber(Number(form.max_clicks))} human clicks` : 'No limit'} />
            <Fact label="Password" value={form.password ? 'Required' : 'None'} />
            <Fact label="Device targets" value={DEVICES.filter((d) => form.targets[d].trim()).map((d) => `${d} → ${form.targets[d].trim()}`).join(', ') || 'None'} />
            {campaignName && <Fact label="Campaign" value={campaignName} />}
            {form.tag_ids.length > 0 && <Fact label="Tags" value={tags.data?.filter((t) => form.tag_ids.includes(t.id)).map((t) => t.name).join(', ') ?? ''} />}
          </dl>
          <IssueList title="Worth knowing before you publish" issues={reviewWarnings} tone="warn" />
          {publish.isError && !(publish.error instanceof ApiError && [409, 422].includes(publish.error.status)) && (
            <p className="form-error" role="alert">
              {describeError(publish.error)}
            </p>
          )}
          <div className="actions">
            <button type="button" className="btn btn-secondary" onClick={() => setStage('options')}>
              Back
            </button>
            <button type="button" className="btn btn-primary" disabled={publish.isPending} onClick={() => publish.mutate()}>
              {publish.isPending ? 'Publishing…' : 'Publish link'}
            </button>
          </div>
        </div>
      )}

      {stage === 'done' && created && (
        <div className="card success">
          <h2 ref={headingRef} tabIndex={-1}>
            Your link is live
          </h2>
          <p className="short-url">
            <a href={created.link.short_url} target="_blank" rel="noreferrer">
              {created.link.short_url}
            </a>
            <CopyButton text={created.link.short_url} />
          </p>
          <IssueList title="Worth knowing" issues={created.warnings ?? []} tone="warn" />
          <div className="qr">
            <img src={api.links.qrUrl(created.link.id, 'svg')} alt={`QR code for ${created.link.short_url}`} width={192} height={192} />
            <div className="actions">
              <a className="btn btn-secondary btn-small" href={api.links.qrUrl(created.link.id, 'png')} download={`${created.link.code}-qr.png`}>
                Download PNG
              </a>
              <a className="btn btn-secondary btn-small" href={api.links.qrUrl(created.link.id, 'svg')} download={`${created.link.code}-qr.svg`}>
                Download SVG
              </a>
            </div>
            <p className="field-hint">Scans are counted separately from clicks: the code opens /{created.link.code}?s=qr.</p>
          </div>
          <div className="actions">
            <RouterLink to={`/links/${created.link.id}`} className="btn btn-primary">
              View details
            </RouterLink>
            <button type="button" className="btn btn-secondary" onClick={restart}>
              Create another
            </button>
            <RouterLink to="/links" className="btn btn-secondary">
              All links
            </RouterLink>
          </div>
        </div>
      )}
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

/** Errors or warnings with their code, so a demo audience can see exactly which rule fired. */
export function IssueList({ title, issues, tone }: { title: string; issues: Issue[]; tone: 'error' | 'warn' }) {
  if (issues.length === 0) return null;
  return (
    <div className={`issues issues-${tone}`} role={tone === 'error' ? 'alert' : undefined}>
      <p className="issues-title">{title}</p>
      <ul>
        {issues.map((i) => (
          <li key={`${i.field}-${i.code}`}>
            <code className="issue-code">{i.code}</code> {explain(i)}
          </li>
        ))}
      </ul>
    </div>
  );
}
