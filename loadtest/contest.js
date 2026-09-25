// k6 scenario of a contest on the reference 2 vCPU machine (see
// README.md): contestants logging in, browsing and submitting (with a
// burst of submissions in the last minutes), their open event streams,
// and ranking spectators.
import http from 'k6/http';
import exec from 'k6/execution';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const CWS = __ENV.CWS || 'http://127.0.0.1:18888';
const RWS = __ENV.RWS || 'http://127.0.0.1:18890';
const CONTEST = __ENV.CONTEST || 'load';
const TASK = __ENV.TASK || 'suma';
const USERS = parseInt(__ENV.CONTESTANTS || '500');
const SPECTATORS = parseInt(__ENV.SPECTATORS || '1000');
// Minutes of normal contest, then of the final burst.
const NORMAL = parseInt(__ENV.NORMAL_MIN || '5');
const BURST = parseInt(__ENV.BURST_MIN || '3');
const PASSWORD = __ENV.PASSWORD || 'load-pw';

const submissions = new Counter('cms_submissions_accepted');
const rejected = new Counter('cms_submissions_rejected');

const total = `${NORMAL + BURST}m`;
export const options = {
  discardResponseBodies: false,
  scenarios: {
    contestants: {
      executor: 'per-vu-iterations', vus: USERS, iterations: 1, maxDuration: `${NORMAL + BURST + 2}m`,
      exec: 'contestant',
    },
    streams: {
      // The open contest page of every contestant (server-sent events).
      executor: 'constant-vus', vus: USERS, duration: total, exec: 'stream', startTime: '20s',
    },
    // Ranking spectators (none with SPECTATORS=0).
    ...(SPECTATORS > 0 ? { spectators: { executor: 'constant-vus', vus: SPECTATORS, duration: total, exec: 'spectator' } } : {}),
  },
  thresholds: {
    'http_req_duration{kind:page}': ['p(95)<150', 'p(99)<400'],
    'http_req_duration{kind:submit}': ['p(95)<300', 'p(99)<800'],
    'http_req_duration{kind:ranking}': ['p(95)<100', 'p(99)<300'],
    'http_req_duration{kind:login}': ['p(95)<2000'],
    'http_req_failed{kind:login}': ['rate<0.01'],
    'http_req_failed{kind:page}': ['rate<0.01'],
    'http_req_failed{kind:submit}': ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
};

// VU numbers are global across scenarios: map them onto the contestants.
function user(vu) { return 'load' + String(((vu - 1) % USERS) + 1).padStart(4, '0'); }

function csrfOf(body) {
  const m = /name="csrf" value="([^"]+)"/.exec(body || '');
  return m ? m[1] : '';
}

// What contestants send: mostly C++ with the whole standard library
// (the slow compile), some lean C++, some Python; a few wrong answers.
const sources = [
  { lang: 'cpp17', ext: 'cpp', weight: 5, src: '#include <bits/stdc++.h>\nusing namespace std;\nint main(){long long a,b;cin>>a>>b;cout<<a+b<<"\\n";}\n' },
  { lang: 'cpp17', ext: 'cpp', weight: 1, src: '#include <bits/stdc++.h>\nusing namespace std;\nint main(){long long a,b;cin>>a>>b;cout<<a-b<<"\\n";}\n' },
  { lang: 'cpp17', ext: 'cpp', weight: 2, src: '#include <cstdio>\nint main(){long long a,b;scanf("%lld %lld",&a,&b);printf("%lld\\n",a+b);}\n' },
  { lang: 'python3', ext: 'py', weight: 2, src: 'a, b = map(int, input().split())\nprint(a + b)\n' },
];
const pool = [].concat(...sources.map((s) => Array(s.weight).fill(s)));

function login(name) {
  const page = http.get(`${CWS}/${CONTEST}/login`, { tags: { kind: 'page', name: 'login-form' } });
  const res = http.post(`${CWS}/${CONTEST}/login`, { csrf: csrfOf(page.body), username: name, password: PASSWORD },
    { tags: { kind: 'login', name: 'login' }, redirects: 5 });
  check(res, { 'logged in': (r) => r.status === 200 && r.body.includes(`/${CONTEST}/tasks/${TASK}`) });
  return csrfOf(res.body);
}

function browse(csrf) {
  const pages = [`/${CONTEST}/`, `/${CONTEST}/tasks/${TASK}`, `/${CONTEST}/tasks/${TASK}/submissions`];
  const url = pages[Math.floor(Math.random() * pages.length)];
  const res = http.get(CWS + url, { tags: { kind: 'page', name: url.replace(/\/[^/]*$/, '/…') } });
  check(res, { 'page ok': (r) => r.status === 200 });
  return csrfOf(res.body) || csrf;
}

function submit(csrf) {
  const s = pool[Math.floor(Math.random() * pool.length)];
  const res = http.post(`${CWS}/${CONTEST}/tasks/${TASK}/submit`, {
    csrf: csrf, language: s.lang, [`${TASK}.%l`]: http.file(s.src, `${TASK}.${s.ext}`, 'text/plain'),
  }, { tags: { kind: 'submit', name: 'submit' }, redirects: 5 });
  if (res.status === 200 && !res.body.includes('class="alert')) submissions.add(1); else rejected.add(1);
  return csrfOf(res.body) || csrf;
}

// contestant: log in (spread over the first minute), browse every 5–15 s
// and submit every 2–4 minutes; in the last BURST minutes (on the test's
// clock) submit every 20–40 s.
export function contestant() {
  const t0 = exec.scenario.startTime;
  const burstAt = t0 + NORMAL * 60 * 1000;
  const endAt = t0 + (NORMAL + BURST) * 60 * 1000 - 10 * 1000;
  sleep(Math.random() * 60);
  let csrf = login(user(__VU));
  let nextSubmit = Date.now() + Math.random() * 120 * 1000;
  while (Date.now() < endAt) {
    const now = Date.now();
    if (now >= nextSubmit) {
      csrf = submit(csrf);
      nextSubmit = now + (now >= burstAt ? 20 + Math.random() * 20 : 120 + Math.random() * 120) * 1000;
    } else {
      csrf = browse(csrf);
    }
    sleep(5 + Math.random() * 10);
  }
}

const streamLogin = {};
// stream: the contestant's open page keeps an event stream; reconnect
// every minute (as browsers do after proxies close idle streams).
export function stream() {
  if (!streamLogin[__VU]) {
    sleep(Math.random() * 60);
    login(user(__VU));
    streamLogin[__VU] = true;
  }
  const res = http.get(`${CWS}/${CONTEST}/events`, { timeout: '60s', responseType: 'none', tags: { kind: 'sse', name: 'events' } });
  // A stream ends by our timeout (status 0 after a minute); a quick end
  // means the session is gone or the server refused it.
  if (res.timings.duration < 1000) {
    streamLogin[__VU] = false;
    sleep(5);
  }
}

const seen = {};
// spectator: the public ranking page, then its event stream.
export function spectator() {
  if (!seen[__VU]) {
    sleep(Math.random() * 30);
    const res = http.get(`${RWS}/${CONTEST}/`, { tags: { kind: 'ranking', name: 'board' } });
    check(res, { 'ranking ok': (r) => r.status === 200 });
    seen[__VU] = true;
  }
  const res = http.get(`${RWS}/${CONTEST}/events`, { timeout: '60s', responseType: 'none', tags: { kind: 'sse', name: 'ranking-events' } });
  if (res.timings.duration < 1000) {
    sleep(5);
  }
  if (Math.random() < 0.1) {
    http.get(`${RWS}/${CONTEST}/`, { tags: { kind: 'ranking', name: 'board' } });
  }
}
