// Formatting and time helpers. The product is built for Indian users, so
// every timestamp is shown in Asia/Kolkata and analytics are bucketed there.

export const TZ = 'Asia/Kolkata';

const dateTimeFmt = new Intl.DateTimeFormat('en-IN', { timeZone: TZ, dateStyle: 'medium', timeStyle: 'short' });
const timeFmt = new Intl.DateTimeFormat('en-IN', { timeZone: TZ, hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
const hourFmt = new Intl.DateTimeFormat('en-IN', { timeZone: TZ, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hour12: false });
const dayFmt = new Intl.DateTimeFormat('en-IN', { timeZone: TZ, day: 'numeric', month: 'short' });
const numberFmt = new Intl.NumberFormat('en-IN');

function parse(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** "20 Oct 2026, 2:05 pm IST", or an em dash when there is no time. */
export function formatDateTime(iso: string | null | undefined): string {
  const d = parse(iso);
  return d ? `${dateTimeFmt.format(d)} IST` : '—';
}

/** "14:05:09" in IST, for "updated at" labels. */
export function formatTime(ms: number): string {
  return timeFmt.format(new Date(ms));
}

/** Axis label for a series bucket: hour buckets show the hour, day buckets only the date. */
export function formatBucket(iso: string, interval: 'hour' | 'day'): string {
  const d = parse(iso);
  if (!d) return iso;
  return interval === 'hour' ? hourFmt.format(d) : dayFmt.format(d);
}

export function formatNumber(n: number | null | undefined): string {
  return typeof n === 'number' && Number.isFinite(n) ? numberFmt.format(n) : '—';
}

/** Share of part in whole as "42%", or an em dash when the whole is zero (no traffic is not 0%). */
export function formatShare(part: number, whole: number): string {
  if (!whole) return '—';
  return `${Math.round((part / whole) * 100)}%`;
}

// ---- periods -------------------------------------------------------------------

export type PeriodKey = '24h' | '7d' | '30d';

export const PERIODS: { key: PeriodKey; label: string; hours: number; interval: 'hour' | 'day' }[] = [
  { key: '24h', label: 'Last 24 hours', hours: 24, interval: 'hour' },
  { key: '7d', label: 'Last 7 days', hours: 24 * 7, interval: 'day' },
  { key: '30d', label: 'Last 30 days', hours: 24 * 30, interval: 'day' },
];

/** RFC 3339 in UTC without milliseconds, the form API.md uses. */
export function toRfc3339(d: Date): string {
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

/**
 * The window for a period, ending now. It is computed when a request is
 * sent rather than when the page renders, so a polling dashboard slides
 * forward instead of freezing at the time the page was opened.
 */
export function periodRange(key: PeriodKey, now = Date.now()): { from: string; to: string } {
  const period = PERIODS.find((p) => p.key === key) ?? PERIODS[1];
  return { from: toRfc3339(new Date(now - period.hours * 3600_000)), to: toRfc3339(new Date(now)) };
}

export function defaultInterval(key: PeriodKey): 'hour' | 'day' {
  return PERIODS.find((p) => p.key === key)?.interval ?? 'day';
}

// ---- IST <-> UTC for datetime-local inputs ---------------------------------------

// India has no daylight saving time, so IST is always exactly UTC+05:30 and
// the conversion needs no timezone database.
const IST_OFFSET_MS = 330 * 60_000;

/** "2026-11-05T23:59" typed as IST → "2026-11-05T18:29:00Z". Empty or malformed input gives null. */
export function istLocalToUtc(value: string): string | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value);
  if (!m) return null;
  const [, y, mo, d, h, mi] = m.map(Number);
  return toRfc3339(new Date(Date.UTC(y, mo - 1, d, h, mi) - IST_OFFSET_MS));
}

/** The inverse of istLocalToUtc, for pre-filling a datetime-local input. */
export function utcToIstLocal(iso: string | null | undefined): string {
  const d = parse(iso);
  if (!d) return '';
  return new Date(d.getTime() + IST_OFFSET_MS).toISOString().slice(0, 16);
}
