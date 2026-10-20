// Redirect latency under load: many visitors following one short link.
//
//   k6 run -e PASSWORD=... loadtest/redirect.js
//
// Each request stops at the 302 (redirects: 0): we measure PathMaxing, not
// the destination website. Read http_req_duration's p(95) in the summary.
import http from 'k6/http';
import { check } from 'k6';
import { BASE, browserUA, createLink, signIn } from './lib.js';

export const options = {
  scenarios: {
    visitors: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: Number(__ENV.VUS || 50) },
        { duration: __ENV.DURATION || '30s', target: Number(__ENV.VUS || 50) },
        { duration: '5s', target: 0 },
      ],
    },
  },
  // A target, not a result: the run fails if p95 goes above it.
  thresholds: {
    http_req_duration: [`p(95)<${__ENV.P95_MS || 100}`],
    checks: ['rate>0.99'],
  },
};

export function setup() {
  signIn();
  return createLink();
}

export default function (link) {
  const res = http.get(`${BASE}/${link.code}`, {
    redirects: 0,
    headers: { 'User-Agent': browserUA },
    tags: { name: 'GET /{code}' },
  });
  check(res, {
    'status is 302': (r) => r.status === 302,
    'not cacheable': (r) => (r.headers['Cache-Control'] || '').includes('no-store'),
  });
}
