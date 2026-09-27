// Drives the contest site in headless Chromium (Playwright) for
// TestContestantUIInBrowser: the code editor's keys (Tab indents, Shift+Tab
// unindents, Esc then Tab leaves it, Ctrl+Enter submits), its draft, the
// checks before sending, the keyboard-only menu, and the right-to-left and
// high-contrast displays. Prints "PASS" or the problems found.
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
    await page.goto(BASE + '/ioi/tasks/sum');
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

    // Keyboard-only: the user menu opens from the keyboard and closes with Esc.
    await page.focus('details.userbox > summary');
    await page.keyboard.press('Enter');
    expect(await page.locator('details.userbox').evaluate(d => d.open), 'the user menu did not open with Enter');
    await page.keyboard.press('Escape');
    expect(!(await page.locator('details.userbox').evaluate(d => d.open)), 'Escape did not close the user menu');

    // Right to left, high contrast, larger text: through the menu's form.
    await page.click('details.userbox > summary');
    await page.selectOption('.menu select[name="lang"]', 'ar');
    await page.selectOption('.menu select[name="theme"]', 'contrast');
    await page.selectOption('.menu select[name="size"]', 'xl');
    await Promise.all([page.waitForNavigation(), page.click('.menu .prefs button')]);
    expect(await page.getAttribute('html', 'dir') === 'rtl', 'the page is not right to left');
    const side = await page.locator('nav.sidebar').boundingBox();
    expect(side && side.x > 640, 'the sidebar should be on the right in RTL (x=' + (side && side.x) + ')');
    const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    expect(bg === 'rgb(0, 0, 0)', 'high contrast background: ' + bg);
    const fs = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize));
    expect(fs > 18, 'larger text: ' + fs + 'px');
    const code = await page.locator('textarea[name="source"]').evaluate(e => getComputedStyle(e).direction);
    expect(code === 'ltr', 'code must stay left to right: ' + code);
    // Narrow screens: the menu button is reachable with the keyboard.
    await page.setViewportSize({ width: 600, height: 900 });
    await page.focus('#nav');
    await page.keyboard.press('Space');
    const shown = await page.locator('nav.sidebar').evaluate(n => getComputedStyle(n).transform);
    expect(shown === 'none' || shown === 'matrix(1, 0, 0, 1, 0, 0)', 'the menu did not open from the keyboard: ' + shown);
  } catch (e) {
    failures.push(String(e && e.stack || e));
  } finally {
    await browser.close();
  }
  console.log(failures.length ? failures.join('\n') : 'PASS');
})();
