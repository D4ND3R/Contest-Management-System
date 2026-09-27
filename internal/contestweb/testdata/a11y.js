// Drives the contest site in headless Chromium (Playwright) for
// TestContestantUIInBrowser: the code editor's keys (Tab indents, Shift+Tab
// unindents, Esc then Tab leaves it, Ctrl+Enter submits), its draft, the
// checks before sending, the notice after sending, the preferences at the
// bottom (applied at once), and the right-to-left and high-contrast
// displays. Prints "PASS" or the problems found.
'use strict';
const { chromium } = require('playwright');
const BASE = process.env.CWS_URL;
const failures = [];
const expect = (ok, what) => { if (!ok) failures.push(what); };

(async () => {
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    await page.goto(BASE + '/ioi/login');
    await page.fill('input[name="username"]', 'ana');
    await page.fill('input[name="password"]', 'secret');
    await page.press('input[name="password"]', 'Enter');
    await page.waitForURL(BASE + '/ioi/');

    // The editor.
    await page.goto(BASE + '/ioi/tasks/sum/submissions');
    await page.click('details.code-editor > summary');
    const ed = page.locator('textarea[name="source"]');
    await ed.click();
    await page.keyboard.type('int main() {');
    await page.keyboard.press('Enter');
    await page.keyboard.press('Tab');
    await page.keyboard.type('return 0;');
    let v = await ed.inputValue();
    expect(v === 'int main() {\n    return 0;', 'Tab should insert four spaces: ' + JSON.stringify(v));
    await page.keyboard.press('Shift+Tab');
    v = await ed.inputValue();
    expect(v === 'int main() {\nreturn 0;', 'Shift+Tab should unindent: ' + JSON.stringify(v));
    await page.keyboard.press('Control+a');
    await page.keyboard.press('Tab');
    v = await ed.inputValue();
    expect(v === '    int main() {\n    return 0;', 'Tab on a selection should indent every line: ' + JSON.stringify(v));
    await page.keyboard.press('Escape');
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => document.activeElement.name !== 'source'), 'Esc then Tab should leave the editor');

    // The draft survives a reload.
    await page.waitForTimeout(600);
    await page.reload();
    expect(await ed.inputValue() === '    int main() {\n    return 0;', 'the draft was not restored');
    expect(await page.locator('details.code-editor').evaluate(d => d.open), 'the editor should open with a draft');

    // Checks before sending: a C file with Python chosen is refused in the page.
    let posted = 0;
    page.on('request', r => { if (r.method() === 'POST' && r.url().endsWith('/submit')) posted++; });
    await page.selectOption('#submit select[name="language"]', 'python3');
    await page.setInputFiles('#submit input[name="sum.%l"]', { name: 'sum.c', mimeType: 'text/plain', buffer: Buffer.from('int main(){}') });
    await page.click('#submit button');
    await page.waitForTimeout(300);
    expect(posted === 0, 'a file with the wrong extension was sent');
    expect((await page.textContent('#submit-result')).includes('extension'), 'no message for the wrong extension');

    // Ctrl+Enter sends the editor's code (the file input emptied first).
    await page.setInputFiles('#submit input[name="sum.%l"]', []);
    await page.selectOption('#submit select[name="language"]', 'c11');
    await ed.click();
    await page.keyboard.press('Control+Enter');
    await page.waitForTimeout(800);
    expect(posted === 1, 'Ctrl+Enter did not submit (' + posted + ' requests)');

    // Sending tells so, and the list shows the new submission.
    expect((await page.textContent('#notifications')).includes('Submission sent'), 'no notice after sending');
    expect(await page.locator('#submissions tbody tr').count() === 1, 'the list does not show the submission');

    // Preferences at the bottom apply at once: theme and size without a
    // reload, the language with one (and the others were remembered).
    await page.selectOption('#prefs select[name="theme"]', 'contrast');
    expect(await page.getAttribute('html', 'data-theme') === 'contrast', 'the theme did not apply at once');
    await page.selectOption('#prefs select[name="size"]', 'xl');
    expect(await page.getAttribute('html', 'data-size') === 'xl', 'the size did not apply at once');
    await page.waitForTimeout(500);
    await Promise.all([page.waitForNavigation(), page.selectOption('#prefs select[name="lang"]', 'ar')]);
    expect(await page.getAttribute('html', 'dir') === 'rtl', 'the page is not right to left');
    expect(await page.getAttribute('html', 'data-theme') === 'contrast', 'the theme was not remembered');
    const first = await page.locator('ul.menu li').first().boundingBox();
    expect(first && first.x > 640, 'the menu should start on the right in RTL (x=' + (first && first.x) + ')');
    const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    expect(bg === 'rgb(0, 0, 0)', 'high contrast background: ' + bg);
    const fs = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize));
    expect(fs > 16, 'larger text: ' + fs + 'px');
    const code = await page.locator('textarea[name="source"]').evaluate(e => getComputedStyle(e).direction);
    expect(code === 'ltr', 'code must stay left to right: ' + code);
    // Narrow screens: nothing scrolls sideways, the menu wraps.
    await page.setViewportSize({ width: 400, height: 900 });
    const wide = await page.evaluate(() => document.documentElement.scrollWidth);
    const culprits = wide > 400 ? await page.evaluate(() => Array.from(document.querySelectorAll('body *')).filter(e => { const r = e.getBoundingClientRect(); return r.width > 0 && (r.left < -1 || r.right > 401); }).slice(0, 8).map(e => e.tagName + '.' + e.className + '#' + e.id + ' ' + Math.round(e.getBoundingClientRect().left) + '..' + Math.round(e.getBoundingClientRect().right)).join(', ')) : '';
    expect(wide <= 400, 'the page scrolls sideways at 400px: ' + wide + ' ' + culprits);
  } catch (e) {
    failures.push(String(e && e.stack || e));
  } finally {
    await browser.close();
  }
  console.log(failures.length ? failures.join('\n') : 'PASS');
})();
