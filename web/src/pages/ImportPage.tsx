import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useState, type FormEvent } from 'react';
import { Link as RouterLink } from 'react-router';
import { ApiError, api } from '../api/client';
import type { ImportJob } from '../api/types';
import { Kpi } from '../components/Overview';
import { describeError } from '../components/States';
import { formatNumber } from '../lib/format';
import { explainCode } from '../lib/issues';
import './bulk.css';

function uploadError(err: unknown): string {
  if (err instanceof ApiError && err.status === 413) return 'This file is too large. An import takes at most 1,000 rows and 1 MB; split it into smaller files.';
  if (err instanceof ApiError && err.status === 429) return `${describeError(err)} Imports are limited to 5 per hour.`;
  return describeError(err);
}

export function ImportPage() {
  const qc = useQueryClient();
  const inputId = useId();
  const [file, setFile] = useState<File | null>(null);
  const [onlyInvalid, setOnlyInvalid] = useState(false);

  const upload = useMutation({ mutationFn: (f: File) => api.imports.upload(f) });
  const commit = useMutation({
    mutationFn: (job: ImportJob) => api.imports.commit(job.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['links'] });
      qc.invalidateQueries({ queryKey: ['campaigns'] });
    },
  });
  const job = upload.data;

  const onUpload = (e: FormEvent) => {
    e.preventDefault();
    if (!file) return;
    commit.reset();
    upload.mutate(file);
  };
  const startOver = () => {
    upload.reset();
    commit.reset();
    setFile(null);
  };

  const rows = job ? (onlyInvalid ? job.rows.filter((r) => r.status === 'invalid') : job.rows) : [];

  return (
    <section aria-labelledby="import-title">
      <div className="page-head">
        <div>
          <p className="kicker">CSV upload</p>
          <h1 id="import-title">Bulk import</h1>
          <p className="muted">Upload a CSV, see every row’s problems before anything is created, then import only the valid rows.</p>
        </div>
      </div>

      {!job && (
        <form className="card" onSubmit={onUpload}>
          <h2>Upload a CSV file</h2>
          <div className="field">
            <label htmlFor={inputId}>CSV file</label>
            <input id={inputId} type="file" accept=".csv,text/csv" aria-describedby={`${inputId}-hint`} onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            <p id={`${inputId}-hint`} className="field-hint">
              Required column: <code>destination</code>. Optional: <code>alias</code>, <code>title</code>, <code>campaign</code> (name), <code>tags</code> (separated by ;), <code>expires_at</code>,{' '}
              <code>max_clicks</code>. At most 1,000 rows and 1 MB.
            </p>
          </div>
          {upload.isError && (
            <p className="form-error" role="alert">
              {uploadError(upload.error)}
            </p>
          )}
          <button type="submit" className="btn btn-primary" disabled={!file || upload.isPending}>
            {upload.isPending ? 'Checking rows…' : 'Upload and preview'}
          </button>
        </form>
      )}

      {job && (
        <>
          <dl className="kpis" aria-label="Import summary">
            <Kpi label="Rows" value={formatNumber(job.total)} />
            <Kpi label="Valid" value={formatNumber(job.valid)} />
            <Kpi label="Invalid" value={formatNumber(job.invalid)} tone={job.invalid > 0 ? 'warn' : undefined} />
          </dl>

          {commit.isSuccess ? (
            <div className="card" role="status">
              <h2>Import finished</h2>
              <p>
                Created <strong>{formatNumber(commit.data.created)}</strong> link{commit.data.created === 1 ? '' : 's'}.
              </p>
              {commit.data.skipped.length > 0 && (
                <>
                  <p>Skipped {formatNumber(commit.data.skipped.length)} rows, including rows that conflicted after the preview:</p>
                  <ul>
                    {commit.data.skipped.map((s) => (
                      <li key={s.row}>
                        Row {s.row}: {s.errors.map((c) => `${c} (${explainCode(c)})`).join('; ')}
                      </li>
                    ))}
                  </ul>
                </>
              )}
              <div className="actions">
                <RouterLink to="/links" className="btn btn-primary">
                  See your links
                </RouterLink>
                <button type="button" className="btn btn-secondary" onClick={startOver}>
                  Import another file
                </button>
              </div>
            </div>
          ) : (
            <div className="card">
              <div className="page-head">
                <h2>Preview</h2>
                <label className="check">
                  <input type="checkbox" checked={onlyInvalid} onChange={(e) => setOnlyInvalid(e.target.checked)} />
                  Only rows with problems
                </label>
              </div>
              <div className="table-wrap">
                <table className="table">
                  <caption className="sr-only">Rows in the uploaded file and their problems</caption>
                  <thead>
                    <tr>
                      <th scope="col" className="num">
                        Row
                      </th>
                      <th scope="col">Alias</th>
                      <th scope="col">Destination</th>
                      <th scope="col">Status</th>
                      <th scope="col">Problems</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((r) => (
                      <tr key={r.row} className={r.status === 'invalid' ? 'row-invalid' : undefined}>
                        <td className="num">{r.row}</td>
                        <td className="mono">{r.alias || <span className="muted">random</span>}</td>
                        <td>
                          <span className="truncate" title={r.destination}>
                            {r.destination}
                          </span>
                        </td>
                        <td>
                          <span className={`badge badge-${r.status === 'valid' ? 'ok' : 'danger'}`}>{r.status === 'valid' ? 'Valid' : 'Invalid'}</span>
                        </td>
                        <td>
                          {r.errors.length === 0 ? (
                            '—'
                          ) : (
                            <ul className="row-errors">
                              {r.errors.map((c) => (
                                <li key={c}>
                                  <code className="issue-code">{c}</code> {explainCode(c)}
                                </li>
                              ))}
                            </ul>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {commit.isError && (
                <p className="form-error" role="alert">
                  {describeError(commit.error)}
                </p>
              )}
              <div className="actions">
                <button type="button" className="btn btn-primary" disabled={job.valid === 0 || commit.isPending} onClick={() => commit.mutate(job)}>
                  {commit.isPending ? 'Importing…' : `Import ${formatNumber(job.valid)} valid row${job.valid === 1 ? '' : 's'}`}
                </button>
                <button type="button" className="btn btn-secondary" onClick={startOver}>
                  Choose another file
                </button>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}
