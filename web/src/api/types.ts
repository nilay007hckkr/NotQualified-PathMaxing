// Types for the PathMaxing HTTP API, written by hand from API.md. When
// API.md changes, change this file in the same commit.

export type Role = 'user' | 'admin';

export interface User {
  id: number;
  email: string;
  name: string;
  role: Role;
  workspace_id: number;
}

export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

// ---- errors and validation --------------------------------------------------

export interface ApiErrorBody {
  error: { code: string; message: string };
}

export interface Issue {
  field: string;
  code: string;
  message: string;
}

export interface ValidationResult {
  errors: Issue[];
  warnings: Issue[];
}

// ---- links ------------------------------------------------------------------

export type LinkStatus = 'active' | 'paused' | 'archived' | 'disabled_admin';
export type TrustState = 'ok' | 'degraded' | 'changed' | 'quarantined';
export type Device = 'mobile' | 'tablet' | 'desktop';

export interface Target {
  device: Device;
  destination: string;
}

export interface LinkInput {
  destination: string;
  alias?: string;
  title?: string;
  starts_at?: string | null;
  expires_at?: string | null;
  max_clicks?: number | null;
  password?: string;
  targets?: Target[];
  campaign_id?: number | null;
  tag_ids?: number[];
  folder_id?: number | null;
}

export interface Link {
  id: number;
  code: string;
  short_url: string;
  destination: string;
  title: string;
  status: LinkStatus;
  trust_state: TrustState;
  starts_at: string | null;
  expires_at: string | null;
  is_expired: boolean;
  max_clicks: number | null;
  cap_used: number;
  has_password: boolean;
  targets: Target[];
  campaign_id: number | null;
  tag_ids: number[];
  folder_id: number | null;
  last_checked_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreateLinkResult {
  link: Link;
  warnings: Issue[];
}

export interface LinkFilters {
  q?: string;
  status?: LinkStatus;
  trust_state?: TrustState;
  campaign_id?: number;
  tag_id?: number;
  folder_id?: number;
  limit?: number;
  cursor?: string;
}

// ---- campaigns, tags, folders ------------------------------------------------

export interface Campaign {
  id: number;
  name: string;
  utm_source: string;
  utm_medium: string;
  utm_campaign: string;
  link_count: number;
  created_at: string;
}

export interface Tag {
  id: number;
  name: string;
  color: string;
}

export interface Folder {
  id: number;
  name: string;
  parent_id: number | null;
}

// ---- bulk import ----------------------------------------------------------------

export interface ImportRow {
  row: number;
  alias: string;
  destination: string;
  status: 'valid' | 'invalid';
  errors: string[];
}

export interface ImportJob {
  id: number;
  status: string;
  total: number;
  valid: number;
  invalid: number;
  rows: ImportRow[];
}

export interface ImportCommitResult {
  created: number;
  skipped: { row: number; errors: string[] }[];
}

// ---- analytics --------------------------------------------------------------------

export type TrafficClass = 'human' | 'email_scanner' | 'link_preview' | 'crawler' | 'other_bot';

export type Outcome =
  | 'redirected'
  | 'expired'
  | 'cap_reached'
  | 'blocked_admin'
  | 'blocked_domain'
  | 'quarantined'
  | 'inactive'
  | 'not_started'
  | 'password_required';

export interface Totals {
  human: number;
  email_scanner: number;
  link_preview: number;
  crawler: number;
  other_bot: number;
  refused: number;
  unique_visitor_days: number;
}

export interface SeriesPoint {
  t: string;
  human: number;
  bots: number;
  refused: number;
}

export interface AnalyticsQuery {
  from?: string;
  to?: string;
  tz?: string;
  interval?: 'hour' | 'day';
}

export interface LinkAnalytics {
  link_id: number;
  tz: string;
  interval: 'hour' | 'day';
  totals: Totals;
  series: SeriesPoint[];
  by_device: Record<string, number>;
  by_browser: Record<string, number>;
  by_os: Record<string, number>;
  by_referrer: { host: string; clicks: number }[];
  by_country: { country: string; clicks: number }[];
  by_source: Record<string, number>;
  outcomes: Record<Outcome, number>;
}

export interface TopLink {
  link_id: number;
  code: string;
  human: number;
}

export interface CampaignAnalytics {
  campaign_id: number;
  tz: string;
  interval: 'hour' | 'day';
  totals: Totals;
  series: SeriesPoint[];
  top_links: TopLink[];
}

export interface Overview {
  totals: Totals;
  top_links: TopLink[];
  campaigns: { id: number; name: string; human: number }[];
  alerts_open: number;
}

export interface LiveClick {
  link_id: number;
  traffic_class: TrafficClass;
  ts: string;
}

// ---- trust and alerts -------------------------------------------------------------

export interface TrustCheck {
  checked_at: string;
  http_status: number;
  final_url: string;
  final_domain: string;
  hops: number;
  verdict: string;
  sources: string[];
}

export interface TrustReport {
  state: TrustState;
  last_checked_at: string | null;
  next_check_at: string | null;
  checks: TrustCheck[];
}

export type AlertKind = 'spike' | 'near_cap' | 'expiring' | 'dead_link' | 'quarantined';

export interface Alert {
  id: number;
  link_id: number;
  kind: AlertKind;
  message: string;
  created_at: string;
}

// ---- link-in-bio ---------------------------------------------------------------

export interface BioPage {
  id: number;
  slug: string;
  title: string;
  link_ids: number[];
}

// ---- admin ------------------------------------------------------------------------

export interface BlockedDomain {
  domain: string;
  reason: string;
  created_at: string;
}

export interface AuditEntry {
  id: number;
  actor_id: number | null;
  action: string;
  entity_type: string;
  entity_id: number;
  detail: Record<string, unknown>;
  ts: string;
}
