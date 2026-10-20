import { Bar, BarChart, CartesianGrid, Cell, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { SeriesPoint, Totals } from '../api/types';
import { formatBucket, formatNumber } from '../lib/format';

// Chart colours are CSS variables (see styles.css) so light and dark mode
// each get their own validated steps instead of an automatic inversion.
export const COLORS = {
  human: 'var(--series-1)',
  bot: 'var(--series-2)',
  refused: 'var(--series-3)',
};

export const CLASS_ROWS: { key: Exclude<keyof Totals, 'unique_visitor_days'>; label: string; group: keyof typeof COLORS }[] = [
  { key: 'human', label: 'Human', group: 'human' },
  // Email scanners publish no signatures, so this label is a heuristic and says so.
  { key: 'email_scanner', label: 'Email scanner (probable)', group: 'bot' },
  { key: 'link_preview', label: 'Link preview', group: 'bot' },
  { key: 'crawler', label: 'Crawler', group: 'bot' },
  { key: 'other_bot', label: 'Other bot', group: 'bot' },
  { key: 'refused', label: 'Refused by a rule', group: 'refused' },
];

export function botTotal(t: Totals): number {
  return t.email_scanner + t.link_preview + t.crawler + t.other_bot;
}

export const tooltipStyle = {
  contentStyle: { background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)' },
  labelStyle: { color: 'var(--text)', fontWeight: 600 },
  itemStyle: { color: 'var(--text)' },
  cursor: { fill: 'var(--surface-2)' },
};

const axisTick = { fill: 'var(--text-muted)', fontSize: 12 };

/**
 * Clicks by traffic class as horizontal bars. Every bar is named on the
 * axis, so colour only groups human / bot / refused and never carries
 * meaning alone; the table below gives the exact numbers.
 */
export function ClassChart({ totals, title = 'Clicks by traffic class' }: { totals: Totals; title?: string }) {
  const data = CLASS_ROWS.map((r) => ({ label: r.label, value: totals[r.key], fill: COLORS[r.group] }));
  const all = data.reduce((s, d) => s + d.value, 0);
  return (
    <figure className="chart">
      <figcaption className="chart-title">{title}</figcaption>
      {all === 0 ? (
        <p className="muted chart-empty">No clicks in this period yet.</p>
      ) : (
        <div className="chart-box" style={{ height: 250 }} aria-hidden="true">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} layout="vertical" margin={{ top: 4, right: 24, bottom: 4, left: 4 }} barCategoryGap={6}>
              <CartesianGrid horizontal={false} stroke="var(--grid)" />
              <XAxis type="number" allowDecimals={false} tick={axisTick} stroke="var(--axis)" />
              <YAxis type="category" dataKey="label" width={150} tick={axisTick} stroke="var(--axis)" />
              <Tooltip {...tooltipStyle} formatter={(v) => [formatNumber(Number(v)), 'Clicks']} />
              <Bar dataKey="value" radius={[0, 4, 4, 0]} isAnimationActive={false}>
                {data.map((d) => (
                  <Cell key={d.label} fill={d.fill} />
                ))}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
      <DataTable caption={title} head={['Traffic class', 'Clicks']} rows={data.map((d) => [d.label, formatNumber(d.value)])} />
    </figure>
  );
}

const SERIES: { key: 'human' | 'bots' | 'refused'; label: string; color: string }[] = [
  { key: 'human', label: 'Human', color: COLORS.human },
  { key: 'bots', label: 'Bots', color: COLORS.bot },
  { key: 'refused', label: 'Refused', color: COLORS.refused },
];

/** Visits over time, stacked by human / bots / refused, bucketed by the API in IST. */
export function SeriesChart({ series, interval, title = 'Visits over time' }: { series: SeriesPoint[]; interval: 'hour' | 'day'; title?: string }) {
  const data = series.map((p) => ({ ...p, label: formatBucket(p.t, interval) }));
  const any = series.some((p) => p.human + p.bots + p.refused > 0);
  return (
    <figure className="chart">
      <figcaption className="chart-title">{title}</figcaption>
      {!any ? (
        <p className="muted chart-empty">No visits in this period yet.</p>
      ) : (
        <div className="chart-box" style={{ height: 280 }} aria-hidden="true">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} margin={{ top: 8, right: 8, bottom: 4, left: -16 }}>
              <CartesianGrid vertical={false} stroke="var(--grid)" />
              <XAxis dataKey="label" tick={axisTick} stroke="var(--axis)" minTickGap={16} />
              <YAxis allowDecimals={false} tick={axisTick} stroke="var(--axis)" />
              <Tooltip {...tooltipStyle} formatter={(v, name) => [formatNumber(Number(v)), name]} />
              <Legend wrapperStyle={{ fontSize: 13, color: 'var(--text-muted)' }} />
              {SERIES.map((s, i) => (
                <Bar
                  key={s.key}
                  dataKey={s.key}
                  name={s.label}
                  stackId="visits"
                  fill={s.color}
                  stroke="var(--surface)"
                  strokeWidth={1}
                  radius={i === SERIES.length - 1 ? [4, 4, 0, 0] : 0}
                  isAnimationActive={false}
                />
              ))}
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
      <DataTable caption={title} head={['Time (IST)', 'Human', 'Bots', 'Refused']} rows={data.map((d) => [d.label, formatNumber(d.human), formatNumber(d.bots), formatNumber(d.refused)])} />
    </figure>
  );
}

/**
 * A ranked list with a proportional bar behind each number. Plain HTML
 * rather than a chart: the label and the exact value are text, so it reads
 * the same to a screen reader as it looks.
 */
export function BarList({ title, items, empty = 'No human clicks yet.' }: { title: string; items: { label: string; value: number }[]; empty?: string }) {
  const sorted = [...items].filter((i) => i.value > 0).sort((a, b) => b.value - a.value);
  const max = sorted[0]?.value ?? 0;
  return (
    <div className="barlist">
      <h3>{title}</h3>
      {sorted.length === 0 ? (
        <p className="muted">{empty}</p>
      ) : (
        <ul>
          {sorted.map((i) => (
            <li key={i.label}>
              <span className="barlist-bar" style={{ width: `${(i.value / max) * 100}%` }} aria-hidden="true" />
              <span className="barlist-label">{i.label}</span>
              <span className="barlist-value">{formatNumber(i.value)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** The accessible twin of a chart: the same numbers as a table, folded away by default. */
export function DataTable({ caption, head, rows }: { caption: string; head: string[]; rows: (string | number)[][] }) {
  return (
    <details className="chart-table">
      <summary>Show as table</summary>
      <div className="table-wrap">
        <table className="table">
          <caption className="sr-only">{caption}</caption>
          <thead>
            <tr>
              {head.map((h) => (
                <th key={h} scope="col">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                {r.map((c, j) => (
                  <td key={j}>{c}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  );
}
