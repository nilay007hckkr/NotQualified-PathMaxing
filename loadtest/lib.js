// Shared helpers: sign in as the demo account and create a link through the
// real API, so the load tests need nothing but a running server.
import http from 'k6/http';

export const BASE = (__ENV.BASE || 'http://localhost:8080').replace(/\/$/, '');
const EMAIL = __ENV.EMAIL || 'loadtest@pathmaxing.example';
const PASSWORD = __ENV.PASSWORD || '';

const json = { headers: { 'Content-Type': 'application/json', Origin: BASE } };

export function signIn() {
  if (PASSWORD.length < 8) throw new Error('set PASSWORD (8+ characters) for the load-test account');
  const creds = JSON.stringify({ email: EMAIL, password: PASSWORD });
  let res = http.post(`${BASE}/api/auth/login`, creds, json);
  if (res.status === 401) {
    res = http.post(`${BASE}/api/auth/signup`, JSON.stringify({ email: EMAIL, password: PASSWORD, name: 'Load test' }), json);
  }
  if (res.status !== 200 && res.status !== 201) throw new Error(`sign in failed: ${res.status} ${res.body}`);
}

// createLink returns {id, code}; the code is random so runs never collide.
export function createLink(extra = {}) {
  const alias = `k6-${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;
  const res = http.post(`${BASE}/api/links`, JSON.stringify({ destination: 'https://example.com/', alias, ...extra }), json);
  if (res.status !== 201) throw new Error(`create link failed: ${res.status} ${res.body}`);
  const link = res.json('link');
  return { id: link.id, code: link.code };
}

export function getLink(id) {
  return http.get(`${BASE}/api/links/${id}`).json('link');
}

export const browserUA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36';
