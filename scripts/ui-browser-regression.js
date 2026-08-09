/* Actual-browser desktop UAT and deterministic brand-canary capture.
 * Usage: node scripts/ui-browser-regression.js http://127.0.0.1:7788 evidence-dir [headed]
 * The report identifies the renderer returned by Puppeteer; it makes no claim
 * that a synthetic renderer or visual model reviewed the screens.
 */
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const {pathToFileURL} = require('url');
const puppeteer = require('puppeteer');

const [url, evidenceDir, mode] = process.argv.slice(2);
if (!url || !evidenceDir) throw new Error('usage: ui-browser-regression.js <url> <evidence-dir> [headed]');
fs.mkdirSync(evidenceDir, {recursive: true});
const root = path.resolve(__dirname, '..');
const contract = JSON.parse(fs.readFileSync(path.join(root, 'docs/product/brand-contract.json'), 'utf8'));
const canaryPath = path.join(root, contract.verification.canary_asset);
const canaryBytes = fs.readFileSync(canaryPath);
const canaryHash = crypto.createHash('sha256').update(canaryBytes).digest('hex');
if (canaryHash !== contract.verification.canary_sha256) throw new Error('brand canary SHA-256 does not match contract');
const requiredViews = contract.desktop_experience.primary_destinations.map(name => name.toLowerCase());
const viewports = contract.desktop_experience.required_viewports;
const summary = {url, mode: mode === 'headed' ? 'headed' : 'headless', canary: {path: contract.verification.canary_asset, sha256: canaryHash, verified: true}, surfaces: [], uncaught_page_errors: [], body_overflow: []};

(async () => {
  const browser = await puppeteer.launch({headless: mode !== 'headed', executablePath: process.env.MIDDEN_BROWSER || undefined, defaultViewport: null});
  try {
    summary.renderer = await browser.version();
    const canaryPage = await browser.newPage();
    await canaryPage.setViewport({width: 1280, height: 240, deviceScaleFactor: 1});
    await canaryPage.goto(pathToFileURL(canaryPath).href, {waitUntil: 'load'});
    const canaryText = await canaryPage.$eval('svg', node => ({role: node.getAttribute('role'), text: node.textContent}));
    if (canaryText.role !== 'img' || !canaryText.text.includes('midden')) throw new Error('brand canary did not render the required wordmark');
    await canaryPage.screenshot({path: path.join(evidenceDir, 'brand-canary-known-good.png')});
    summary.canary.screenshot = 'brand-canary-known-good.png';
    await canaryPage.close();

    for (const viewport of viewports) {
      const name = `${viewport.width}x${viewport.height}`;
      const page = await browser.newPage();
      await page.setViewport({width: viewport.width, height: viewport.height, deviceScaleFactor: 1});
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto(url, {waitUntil: 'networkidle0'});
      const nav = await page.$$eval('[data-view]', nodes => nodes.map(node => ({view: node.dataset.view, label: node.textContent.trim()})));
      for (const view of requiredViews) if (!nav.some(item => item.view === view)) throw new Error(`${name}: missing ${view} primary destination`);
      const sidebar = await page.$eval('#sidebar', node => { const box = node.getBoundingClientRect(); return {visible: box.width > 150 && box.left >= 0, width: box.width}; });
      if (!sidebar.visible) throw new Error(`${name}: persistent desktop sidebar is not visible`);
      for (const view of requiredViews) {
        await page.click(`[data-view="${view}"]`);
        await page.waitForSelector(`#${view}-content .page-head`);
        const overflow = await page.evaluate(() => document.body.scrollWidth > document.documentElement.clientWidth);
        const screenshot = `${name}-${view}.png`;
        await page.screenshot({path: path.join(evidenceDir, screenshot), fullPage: true});
        summary.surfaces.push({viewport, view, screenshot});
        summary.body_overflow.push({viewport, view, overflow});
        if (overflow) throw new Error(`${name}/${view}: horizontal body overflow`);
      }
      if (errors.length) { summary.uncaught_page_errors.push({viewport, errors}); throw new Error(`${name}: page errors: ${errors.join('; ')}`); }
      await page.close();
    }
    if (summary.surfaces.length !== viewports.length * requiredViews.length) throw new Error('incomplete all-surface capture');
    fs.writeFileSync(path.join(evidenceDir, 'uat-summary.json'), JSON.stringify(summary, null, 2) + '\n');
    console.log(JSON.stringify(summary, null, 2));
  } finally { await browser.close(); }
})().catch(error => { console.error(error.stack || error); process.exit(1); });
