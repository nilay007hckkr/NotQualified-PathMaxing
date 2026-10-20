// A small typed client for the PathMaxing API. Every request goes to the
// same origin with the session cookie, so the browser sends our Origin
// header and the server's CSRF check passes without extra tokens.

import type {
  Alert,
  AnalyticsQuery,
  AuditEntry,
  BioPage,
  BlockedDomain,
  Campaign,
  CampaignAnalytics,
  CreateLinkResult,
  Folder,
  ImportCommitResult,
  ImportJob,
  Link,
  LinkAnalytics,
  LinkFilters,
  LinkInput,
  Overview,
  Page,
  Tag,
  TrustReport,
  User,
  ValidationResult,
} from './types';

/** An error response from the API, with the parts the UI needs to explain it. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** Present on 422 responses: field-level errors and warnings. */
  readonly validation?: ValidationResult;
  /** Seconds to wait, from Retry-After on 429 responses. */
  readonly retryAfter?: number;

  constructor(status: number, code: string, message: string, validation?: ValidationResult, retryAfter?: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.validation = validation;
    this.retryAfter = retryAfter;
  }
}

type Query = Record<string, string | number | boolean | undefined | null>;

/** Builds "?a=1&b=2", leaving out empty values. */
export function queryString(params: Query = {}): string {
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') qs.set(key, String(value));
  }
  const s = qs.toString();
  return s ? `?${s}` : '';
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  let payload: BodyInit | undefined;
  if (body instanceof FormData) {
    payload = body; // the browser sets the multipart boundary
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }

  let res: Response;
  try {
    res = await fetch(`/api${path}`, { method, headers, body: payload, credentials: 'same-origin' });
  } catch {
    throw new ApiError(0, 'NETWORK', 'Could not reach the server. Check your connection and try again.');
  }

  const text = res.status === 204 ? '' : await res.text();
  let data: unknown;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }
  if (!res.ok) throw toApiError(res, data);
  return data as T;
}

function toApiError(res: Response, data: unknown): ApiError {
  const retry = Number(res.headers.get('Retry-After'));
  const retryAfter = Number.isFinite(retry) && retry > 0 ? retry : undefined;
  if (data && typeof data === 'object') {
    if ('errors' in data && Array.isArray((data as ValidationResult).errors)) {
      const v = data as ValidationResult;
      const first = v.errors[0];
      return new ApiError(res.status, first?.code ?? 'VALIDATION', first?.message ?? 'Please fix the highlighted fields.', { errors: v.errors, warnings: v.warnings ?? [] }, retryAfter);
    }
    if ('error' in data) {
      const e = (data as { error: { code?: string; message?: string } }).error;
      return new ApiError(res.status, e?.code ?? 'ERROR', e?.message ?? res.statusText, undefined, retryAfter);
    }
  }
  return new ApiError(res.status, `HTTP_${res.status}`, res.statusText || `Request failed with status ${res.status}`, undefined, retryAfter);
}

const get = <T>(path: string) => request<T>('GET', path);
const post = <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {});
const patch = <T>(path: string, body: unknown) => request<T>('PATCH', path, body);
const del = (path: string) => request<void>('DELETE', path);

export const api = {
  auth: {
    signup: (input: { email: string; password: string; name: string }) => post<{ user: User }>('/auth/signup', input),
    login: (input: { email: string; password: string }) => post<{ user: User }>('/auth/login', input),
    logout: () => post<void>('/auth/logout'),
    me: () => get<{ user: User }>('/auth/me'),
  },

  links: {
    validate: (input: LinkInput) => post<ValidationResult>('/links/validate', input),
    create: (input: LinkInput) => post<CreateLinkResult>('/links', input),
    list: (filters: LinkFilters = {}) => get<Page<Link>>(`/links${queryString({ ...filters })}`),
    get: (id: number) => get<{ link: Link }>(`/links/${id}`),
    update: (id: number, input: Partial<LinkInput>) => patch<{ link: Link }>(`/links/${id}`, input),
    pause: (id: number) => post<{ link: Link }>(`/links/${id}/pause`),
    resume: (id: number) => post<{ link: Link }>(`/links/${id}/resume`),
    archive: (id: number) => post<{ link: Link }>(`/links/${id}/archive`),
    remove: (id: number) => del(`/links/${id}`),
    qrUrl: (id: number, format: 'png' | 'svg' = 'svg', size = 512) => `/api/links/${id}/qr${queryString({ format, size })}`,
    trust: (id: number) => get<TrustReport>(`/links/${id}/trust`),
    rescan: (id: number) => post<{ queued: boolean }>(`/links/${id}/rescan`),
  },

  campaigns: {
    list: () => get<Page<Campaign>>('/campaigns'),
    get: (id: number) => get<{ campaign: Campaign }>(`/campaigns/${id}`),
    create: (input: Partial<Campaign> & { name: string }) => post<{ campaign: Campaign }>('/campaigns', input),
    update: (id: number, input: Partial<Campaign>) => patch<{ campaign: Campaign }>(`/campaigns/${id}`, input),
    remove: (id: number) => del(`/campaigns/${id}`),
  },
  tags: {
    list: () => get<Page<Tag>>('/tags'),
    create: (input: { name: string; color?: string }) => post<{ tag: Tag }>('/tags', input),
    update: (id: number, input: Partial<Tag>) => patch<{ tag: Tag }>(`/tags/${id}`, input),
    remove: (id: number) => del(`/tags/${id}`),
  },
  folders: {
    list: () => get<Page<Folder>>('/folders'),
    create: (input: { name: string; parent_id?: number | null }) => post<{ folder: Folder }>('/folders', input),
    update: (id: number, input: Partial<Folder>) => patch<{ folder: Folder }>(`/folders/${id}`, input),
    remove: (id: number) => del(`/folders/${id}`),
  },

  imports: {
    upload: (file: File) => {
      const form = new FormData();
      form.append('file', file);
      return post<ImportJob>('/imports', form);
    },
    get: (id: number) => get<ImportJob>(`/imports/${id}`),
    commit: (id: number) => post<ImportCommitResult>(`/imports/${id}/commit`),
  },

  analytics: {
    link: (id: number, q: AnalyticsQuery = {}) => get<LinkAnalytics>(`/analytics/links/${id}${queryString({ ...q })}`),
    campaign: (id: number, q: AnalyticsQuery = {}) => get<CampaignAnalytics>(`/analytics/campaigns/${id}${queryString({ ...q })}`),
    overview: (q: AnalyticsQuery = {}) => get<Overview>(`/analytics/overview${queryString({ ...q })}`),
    liveUrl: (linkId?: number) => `/api/analytics/live${queryString({ link_id: linkId })}`,
  },

  alerts: {
    list: (status: 'open' | 'all' = 'open') => get<{ items: Alert[] }>(`/alerts${queryString({ status })}`),
  },

  bioPages: {
    list: () => get<Page<BioPage>>('/bio-pages'),
    create: (input: { slug: string; title: string; link_ids: number[] }) => post<{ bio_page: BioPage }>('/bio-pages', input),
    update: (id: number, input: Partial<BioPage>) => patch<{ bio_page: BioPage }>(`/bio-pages/${id}`, input),
  },

  admin: {
    links: (filters: Pick<LinkFilters, 'status' | 'trust_state' | 'q' | 'cursor' | 'limit'> = {}) =>
      get<Page<Link>>(`/admin/links${queryString({ ...filters })}`),
    disable: (id: number, reason: string) => post<{ link: Link }>(`/admin/links/${id}/disable`, { reason }),
    enable: (id: number) => post<{ link: Link }>(`/admin/links/${id}/enable`),
    abuse: () => get<Page<Link>>('/admin/abuse'),
    blocklist: () => get<Page<BlockedDomain>>('/admin/blocklist'),
    block: (domain: string, reason: string) => post<{ blocked_domain: BlockedDomain }>('/admin/blocklist', { domain, reason }),
    unblock: (domain: string) => del(`/admin/blocklist/${encodeURIComponent(domain)}`),
    audit: (cursor?: string, limit = 50) => get<Page<AuditEntry>>(`/admin/audit${queryString({ cursor, limit })}`),
    users: () => get<Page<User & { created_at: string; link_count: number }>>('/admin/users'),
    overview: (q: AnalyticsQuery = {}) => get<Overview>(`/admin/analytics/overview${queryString({ ...q })}`),
  },
};
