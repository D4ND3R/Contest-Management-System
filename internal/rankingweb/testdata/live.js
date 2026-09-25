// Drives the live ranking page in headless Chromium (Playwright) for
// TestLivePageInBrowser: random score changes pushed to the ranking web
// server at RWS_URL, a dropped connection and an unfreeze. After each step
// the table must show every row with its right rank, in order, and the
// page must reload only when it missed an update. Prints "PASS" or the
// problems found.
'use strict';
const { chromium } = require('playwright');
const RWS = process.env.RWS_URL, HDR = { Authorization: 'Bearer ' + process.env.RWS_TOKEN, 'Content-Type': 'application/json' };
const failures = [];
let seed = 7;
const rnd = n => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % n; };
const sleep = ms => new Promise(r => setTimeout(r, ms));

// The board: 40 rows with names that repeat (ties are ordered by name).
const names = ['Ana', 'Beto', 'Caro', 'Dani', 'Eva', 'Fede', 'Gabi', 'Hugo', 'Ines', 'Juan', 'Ana', 'Lalo'];
let rows = [];
for (let i = 0; i < 40; i++) rows.push({ key: 'p' + i, name: names[i % names.length] + (i >= 12 ? ' ' + i : ''), total: 0 });
function ranked(rs) {
  const s = rs.map(r => ({ ...r })).sort((a, b) => b.total - a.total || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  s.forEach((r, i) => { r.rank = i > 0 && s[i - 1].total === r.total ? s[i - 1].rank : i + 1; });
  return s;
}
const wire = r => ({ key: r.key, rank: r.rank, name: r.name, total: r.total, cells: [{ score: r.total, submitted: r.total > 0 }] });
const board = (rs, frozen) => ({ contest: 'live', title: 'Live', precision: 0, start: '2030-01-01T10:00:00Z', stop: '2030-01-01T15:00:00Z',
  timezone: 'UTC', frozen: !!frozen, tasks: [{ name: 'a', title: 'A', max_score: 1000 }], rows: rs.map(wire) });
// following waits until n pages follow the board's events.
async function following(n) {
  for (const deadline = Date.now() + 10000; Date.now() < deadline; await sleep(50)) {
    if (Number(await (await fetch(RWS + '/test/spectators')).text()) === n) return;
  }
  throw new Error(`not ${n} spectators`);
}
async function push(msg) {
  const r = await fetch(RWS + '/push', { method: 'POST', headers: HDR, body: JSON.stringify(msg) });
  if (r.status !== 200) throw new Error('push: ' + r.status);
}

// A step changes one to three scores (sometimes down), now and then
// removes or adds a row, and pushes the rows that changed, as the pusher.
let seq = 1, cur = ranked(rows);
async function step() {
  const prev = new Map(cur.map(r => [r.key, JSON.stringify(wire(r))]));
  for (let i = 1 + rnd(3); i > 0; i--) {
    const r = rows[rnd(rows.length)];
    r.total = Math.max(0, r.total + (rnd(5) === 0 ? -rnd(50) : rnd(100)));
  }
  let removed = [];
  if (rnd(15) === 0 && rows.length > 10) removed = [rows.splice(rnd(rows.length), 1)[0].key];
  if (rnd(15) === 0) rows.push({ key: 'n' + seq, name: 'Nuevo ' + seq, total: rnd(300) });
  cur = ranked(rows);
  seq++;
  await push({ contest: 'live', kind: 'delta', seq, base: seq - 1, removed,
    rows: cur.filter(r => prev.get(r.key) !== JSON.stringify(wire(r))).map(wire) });
}

// check waits (up to 10 s) for the table to match the board.
async function check(page, what) {
  let problems = [];
  for (const deadline = Date.now() + 10000; Date.now() < deadline; await sleep(100)) {
    const dom = await page.$$eval('#rows tr', trs => trs.map(t => [t.dataset.key, Number(t.dataset.rank), t.querySelector('.rank').textContent.trim(), t.dataset.name]))
      .catch(() => null); // the page may be reloading
    if (!dom) continue;
    const want = new Map(cur.map(r => [r.key, r.rank]));
    problems = [];
    if (dom.length !== cur.length) problems.push(`${dom.length} rows, want ${cur.length}`);
    dom.forEach(([k, r, txt, name], i) => {
      if (want.get(k) !== r || String(r) !== txt) problems.push(`${k} at rank ${r} (${txt}), want ${want.get(k)}`);
      if (i > 0) {
        const [, pr, , pn] = dom[i - 1];
        if (pr > r || (pr === r && pn > name)) problems.push(`row ${i} out of order`);
      }
    });
    if (!problems.length) return;
  }
  failures.push(what + ': ' + problems.slice(0, 5).join('; '));
}

(async () => {
  await push({ contest: 'live', kind: 'full', seq, board: board(cur) });
  const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});
  try {
    const page = await browser.newPage();
    let loads = 0;
    page.on('load', () => loads++);
    page.on('pageerror', e => failures.push('script error: ' + e.message));
    await page.goto(RWS + '/live/');
    await following(1);
    for (let round = 0; round < 12; round++) {
      for (let i = 0; i < 10; i++) { await step(); await sleep(20); }
      await check(page, 'live updates, round ' + round);
    }
    if (loads !== 1) failures.push(`the page reloaded ${loads - 1} times during live updates`);

    // The connection drops and updates go by meanwhile: back, the page
    // notices it missed them and reloads.
    await fetch(RWS + '/test/drop', { method: 'POST' });
    await following(0);
    for (let i = 0; i < 3; i++) await step();
    await following(1); // the browser reconnects after 3 s
    await step();
    await check(page, 'after a dropped connection');
    if (loads < 2) failures.push('the page did not reload after missing updates');

    // Freeze (a reload), scores change unseen, unfreeze: the changed rows
    // are revealed one by one and the table ends right.
    seq++;
    await push({ contest: 'live', kind: 'full', seq, board: board(cur, true) });
    const frozenAt = loads;
    for (const deadline = Date.now() + 10000; loads === frozenAt && Date.now() < deadline;) await sleep(100);
    await following(1);
    for (let i = 0; i < 4; i++) rows[rnd(rows.length)].total += 1 + rnd(200);
    cur = ranked(rows);
    seq++;
    await push({ contest: 'live', kind: 'full', seq, board: board(cur) });
    await sleep(Math.min(30000, 700 * cur.length));
    await check(page, 'after unfreezing');
  } finally {
    await browser.close();
  }
  console.log(failures.length ? failures.join('\n') : 'PASS');
})().catch(e => { console.log('ERROR ' + e.stack); });
