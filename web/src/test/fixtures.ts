// Shared API fixtures for tests. Kept out of *.test files so importing
// them doesn't register another file's tests twice.
import type { Link } from '../api/types';

export const sampleLink: Link = {
  id: 42,
  code: 'diwali',
  short_url: 'https://pm.example/diwali',
  destination: 'http://shop.example/sale',
  title: 'Diwali sale',
  status: 'active',
  trust_state: 'ok',
  starts_at: null,
  expires_at: '2026-11-05T18:29:00Z',
  is_expired: false,
  max_clicks: 100,
  cap_used: 12,
  has_password: false,
  targets: [],
  campaign_id: null,
  tag_ids: [],
  folder_id: null,
  last_checked_at: '2026-10-20T08:00:00Z',
  created_at: '2026-10-18T10:00:00Z',
  updated_at: '2026-10-18T10:00:00Z',
};
