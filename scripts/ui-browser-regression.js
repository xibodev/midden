/* Optional local browser regression for the embedded UI.
 * Usage: NODE_PATH=<puppeteer parent> node scripts/ui-browser-regression.js http://127.0.0.1:7788 evidence-dir [headed]
 * It is intentionally dependency-free in the Go product; Puppeteer is only a
 * local test harness supplied by the operator environment.
 */
const fs = require('fs');
const path = require('path');
const puppeteer = require('puppeteer');

const [url, evidenceDir, mode] = process.argv.slice(2);
if (!url || !evidenceDir) throw new Error('usage: ui-browser-regression.js <url> <evidence-dir> [headed]');
fs.mkdirSync(evidenceDir, {recursive: true});

(async () => {
  const browser = await puppeteer.launch({
    headless: mode !== 'headed',
    executablePath: process.env.MIDDEN_BROWSER || undefined,
    defaultViewport: null,
  });
  try {
    for (const [name, width, height] of [['desktop', 1440, 900], ['compact-desktop', 1024, 768]]) {
      const page = await browser.newPage();
      await page.setViewport({width, height, deviceScaleFactor: 1});
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto(url, {waitUntil: 'networkidle0'});
      await page.waitForSelector('#recover-content .page-head');
      const sidebarVisible = await page.$eval('#sidebar', node => {
        const box = node.getBoundingClientRect();
        return box.width > 150 && box.left >= 0;
      });
      if (!sidebarVisible) throw new Error(`${name}: persistent desktop sidebar is not visible`);
      await page.click('[data-view="activity"]');
      await page.waitForSelector('#activity-content .page-head');
      await page.screenshot({path: path.join(evidenceDir, `${name}-activity.png`), fullPage: true});
      if (errors.length) throw new Error(`${name}: page errors: ${errors.join('; ')}`);
      await page.close();
    }
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error.stack || error); process.exit(1); });
