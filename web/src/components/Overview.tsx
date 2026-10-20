import type { ReactNode } from 'react';
import type { Overview } from '../api/types';
import { formatNumber, formatShare } from '../lib/format';
import { ClassChart, botTotal } from './charts';
import { EmptyState } from './States';

export function Kpi({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: 'warn' }) {
  return (
    <div className={`kpi${tone ? ` kpi-${tone}` : ''}`}>
      <dt>{label}</dt>
      <dd>
        <span className="kpi-value">{value}</span>
        {hint && <span className="kpi-hint">{hint}</span>}
      </dd>
    </div>
  );
}

/**
 * The KPI row, the human-vs-bot chart, top links and campaigns for one
 * overview response. Shared by the user dashboard and the admin platform
 * view, which differ only in scope.
 */
export function OverviewBody({ data, linkTo }: { data: Overview; linkTo?: (linkId: number, code: string) => ReactNode }) {
  const t = data.totals;
  const bots = botTotal(t);
  const all = t.human + bots + t.refused;
  return (
    <>
      <dl className="kpis" aria-label="Key numbers">
        <Kpi label="Human clicks" value={formatNumber(t.human)} hint={`${formatShare(t.human, all)} of all visits`} />
        <Kpi label="Bot visits" value={formatNumber(bots)} hint="scanners, previews, crawlers" />
        <Kpi label="Refused visits" value={formatNumber(t.refused)} hint="blocked, expired, capped…" />
        <Kpi label="Unique visitor-days" value={formatNumber(t.unique_visitor_days)} hint="hash rotates daily" />
        <Kpi label="Open alerts" value={formatNumber(data.alerts_open)} tone={data.alerts_open > 0 ? 'warn' : undefined} />
      </dl>

      <div className="grid-2">
        <section className="card" aria-labelledby="ov-classes">
          <h2 id="ov-classes">Human vs bot</h2>
          <ClassChart totals={t} title="Visits by traffic class" />
        </section>

        <section className="card" aria-labelledby="ov-top">
          <h2 id="ov-top">Top links</h2>
          {data.top_links.length === 0 ? (
            <EmptyState title="No human clicks in this period." />
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
                  {data.top_links.map((l) => (
                    <tr key={l.link_id}>
                      <td className="mono">{linkTo ? linkTo(l.link_id, l.code) : `/${l.code}`}</td>
                      <td className="num">{formatNumber(l.human)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>

      <section className="card" aria-labelledby="ov-campaigns">
        <h2 id="ov-campaigns">Campaigns</h2>
        {data.campaigns.length === 0 ? (
          <EmptyState title="No campaign traffic in this period." />
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Campaign</th>
                  <th scope="col" className="num">
                    Human clicks
                  </th>
                </tr>
              </thead>
              <tbody>
                {data.campaigns.map((c) => (
                  <tr key={c.id}>
                    <td>{c.name}</td>
                    <td className="num">{formatNumber(c.human)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
