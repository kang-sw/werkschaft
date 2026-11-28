// Run by TestFirstPaintShellFrameBrowser against served shell and framed session pages.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);

function decodePNG(buf) {
  assert.equal(buf.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', 'valid PNG signature');
  let pos = 8, width = 0, height = 0, bpp = 0;
  const idat = [];
  while (pos < buf.length) {
    const len = buf.readUInt32BE(pos);
    const type = buf.subarray(pos + 4, pos + 8).toString('ascii');
    const data = buf.subarray(pos + 8, pos + 8 + len);
    pos += 12 + len;
    if (type === 'IHDR') {
      width = data.readUInt32BE(0);
      height = data.readUInt32BE(4);
      const depth = data[8], color = data[9];
      assert.equal(depth, 8, '8-bit PNG');
      bpp = color === 6 ? 4 : color === 2 ? 3 : 0;
      assert(bpp > 0, `unsupported PNG color type ${color}`);
    } else if (type === 'IDAT') {
      idat.push(data);
    } else if (type === 'IEND') {
      break;
    }
  }
  const raw = zlib.inflateSync(Buffer.concat(idat));
  const stride = width * bpp;
  const pixels = Buffer.alloc(height * stride);
  const paeth = (a, b, c) => {
    const p = a + b - c;
    const pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c);
    return pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
  };
  for (let y = 0; y < height; y++) {
    const rowStart = y * (1 + stride);
    const filter = raw[rowStart];
    const out = y * stride;
    for (let x = 0; x < stride; x++) {
      const v = raw[rowStart + 1 + x];
      const left = x >= bpp ? pixels[out + x - bpp] : 0;
      const up = y > 0 ? pixels[out - stride + x] : 0;
      const upLeft = x >= bpp && y > 0 ? pixels[out - stride + x - bpp] : 0;
      let val = v;
      if (filter === 1) val = (v + left) & 0xff;
      else if (filter === 2) val = (v + up) & 0xff;
      else if (filter === 3) val = (v + ((left + up) >> 1)) & 0xff;
      else if (filter === 4) val = (v + paeth(left, up, upLeft)) & 0xff;
      pixels[out + x] = val;
    }
  }
  return { width, height, bpp, pixels };
}

(async () => {
  if (shots) fs.mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const cases = [
    { name: 'dark', cookie: 'dark', colorScheme: 'light', wantBg: [22, 24, 29], forbidWhite: true },
    { name: 'auto-dark', cookie: '', colorScheme: 'dark', wantBg: [22, 24, 29], forbidWhite: true },
    { name: 'light', cookie: 'light', colorScheme: 'dark', wantBg: [244, 244, 241], forbidWhite: false }
  ];
  try {
    for (const c of cases) {
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, colorScheme: c.colorScheme });
      if (c.cookie) await context.addCookies([{ name: 'ws_dashboard_theme', value: c.cookie, url: base }]);
      const tab = await context.newPage();
      const cdp = await context.newCDPSession(tab);
      const errors = [];
      tab.on('pageerror', e => errors.push(e.message));
      let releaseCSS, sawCSS;
      const cssReleased = new Promise(r => { releaseCSS = r; });
      const cssSeen = new Promise(r => { sawCSS = r; });
      let sawOnce = false;
      await tab.route('**/s/dashboard-*.css', async route => {
        if (!sawOnce) { sawOnce = true; sawCSS(); }
        await cssReleased;
        await route.continue();
      });
      const nav = tab.goto(base + '/s/livesession', { waitUntil: 'domcontentloaded' });
      await cssSeen;
      await tab.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
      const box = await tab.evaluate(() => {
        const rail = document.getElementById('rail')?.getBoundingClientRect();
        const top = document.querySelector('.dwrap > .top')?.getBoundingClientRect();
        const well = document.getElementById('stage-well-bg')?.getBoundingClientRect();
        const frame = document.querySelector('iframe.sframe.on')?.getBoundingClientRect();
        const left = Math.ceil((rail && rail.width > 0 ? rail.right : 0) + 12);
        // .top floating pills cast a drop shadow several pixels below .top.bottom.
        const minY = Math.ceil((top ? top.bottom : 0) + 28);
        return {
          left: Math.max(left, Math.ceil((well?.left || 0) + 8), Math.ceil(frame?.left || 0)),
          top: Math.max(minY, Math.ceil((well?.top || 0) + 8), Math.ceil(frame?.top || 0)),
          right: Math.floor(Math.min(well ? well.right : window.innerWidth, frame?.right || window.innerWidth) - 8),
          bottom: Math.floor(Math.min(well ? well.bottom : window.innerHeight, frame?.bottom || window.innerHeight) - 8)
        };
      });
      const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
      const pngBuf = Buffer.from(data, 'base64');
      if (shots) fs.writeFileSync(path.join(shots, `first-paint-${c.name}.png`), pngBuf);
      const img = decodePNG(pngBuf);
      assert.equal(img.width, 1280, `${c.name}: expected 1280px capture width`);
      assert.equal(img.height, 800, `${c.name}: expected 800px capture height`);
      let whiteCount = 0, total = 0, mismatchCount = 0;
      for (let y = box.top; y < box.bottom; y += 4) {
        for (let x = box.left; x < box.right; x += 4) {
          const i = (y * img.width + x) * img.bpp;
          const r = img.pixels[i], g = img.pixels[i + 1], b = img.pixels[i + 2];
          total++;
          if (r > 240 && g > 240 && b > 240) whiteCount++;
          if (Math.abs(r - c.wantBg[0]) > 2 || Math.abs(g - c.wantBg[1]) > 2 || Math.abs(b - c.wantBg[2]) > 2) mismatchCount++;
        }
      }
      assert(total > 1000, `${c.name}: sampled frame area (${total} samples)`);
      if (c.forbidWhite) assert.equal(whiteCount, 0, `${c.name}: expected 0 white pixels in frame area, got ${whiteCount}/${total}`);
      assert.equal(mismatchCount, 0, `${c.name}: expected all frame pixels to match rgb(${c.wantBg.join(',')}), got ${mismatchCount}/${total} mismatches`);
      releaseCSS();
      await nav;
      const frame = await (await tab.locator('iframe.sframe.on').elementHandle()).contentFrame();
      await frame.waitForFunction(() => document.readyState === 'complete');
      assert.deepEqual(errors, [], `${c.name}: no uncaught browser errors`);
      console.log(`PASS ${c.name}: ${total} frame samples matched rgb(${c.wantBg.join(',')}), white=${whiteCount}`);
      await context.close();
    }
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
