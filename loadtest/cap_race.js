// The click-cap race: 1,000 visitors hit a link capped at CAP clicks at the
// same moment. Exactly CAP must be redirected and the rest refused with 410,
// and cap_used must end at exactly CAP.
//
//   k6 run -e PASSWORD=... loadtest/cap_race.js        (server: TRUSTED_PROXY_HOPS=1)
//
// Every request sends its own X-Forwarded-For from a random /24. Without
// that, 1,000 requests from one machine in a second look like a mail
// scanner burst to the classifier, and bots never use up a cap.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';
import { BASE, browserUA, createLink, getLink, signIn } from './lib.js';

const CAP = Number(__ENV.CAP || 100);
const VISITORS = Number(__ENV.VISITORS || 1000);

const redirected = new Counter('cap_redirected');
const refused = new Counter('cap_refused');

export const options = {
  scenarios: {
    race: { executor: 'shared-iterations', vus: VISITORS, iterations: VISITORS, maxDuration: '60s' },
  },
  thresholds: {
    cap_redirected: [`count==${CAP}`],
    cap_refused: [`count==${VISITORS - CAP}`],
  },
};

export function setup() {
  signIn();
  return createLink({ max_clicks: CAP });
}

export default function (link) {
  // A random /24 per request, so neither this run nor the previous one can
  // add up to a burst from one network (which would make these bot visits).
  const r = () => Math.floor(Math.random() * 250) + 1;
  const ip = `${r()}.${r()}.${r()}.10`;
  const params = { redirects: 0, headers: { 'User-Agent': browserUA, 'X-Forwarded-For': ip } };
  let res = http.get(`${BASE}/${link.code}`, params);
  // A thousand connections opened in the same instant can overflow the
  // server's TCP accept queue (Windows' default is small), and the OS
  // refuses some before the server ever sees them. Those never reached
  // PathMaxing, so retry them; every request still races for the cap.
  for (let attempt = 1; res.status === 0 && attempt <= 5; attempt++) {
    sleep(0.05 * attempt);
    res = http.get(`${BASE}/${link.code}`, params);
  }
  if (res.status === 302) redirected.add(1);
  else if (res.status === 410) refused.add(1);
  check(res, { '302 or 410': (r) => r.status === 302 || r.status === 410 });
}

export function teardown(link) {
  signIn();
  const after = getLink(link.id);
  console.log(`cap_used after the race: ${after.cap_used} (cap ${CAP})`);
  if (after.cap_used !== CAP) throw new Error(`cap_used is ${after.cap_used}, want exactly ${CAP}`);
}
