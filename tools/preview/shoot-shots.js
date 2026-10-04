const { chromium } = require('playwright-core');
const path = require('path');
const fs = require('fs');

const FILE = 'file:///' + path.resolve('E:/plumb/media/promo-motion.html').replace(/\\/g, '/');
const OUT = 'E:/plumb/media/.shots';
fs.mkdirSync(OUT, { recursive: true });

(async () => {
  const browser = await chromium.launch({
    executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe',
    args: ['--force-color-profile=srgb', '--font-render-hinting=none', '--hide-scrollbars'],
  });
  // 1080p, so every clamp() resolves to its designed maximum — this is the
  // resolution the video will actually be recorded at.
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
  const errors = [];
  page.on('pageerror', e => errors.push('PAGEERROR: ' + e.message));

  await page.goto(FILE, { waitUntil: 'load' });
  // Strip the surrounding page so the stage itself is the full 1920x1080 frame.
  await page.addStyleTag({
    content: `body{padding:0!important;margin:0!important}
      .wrap{max-width:none!important;padding:0!important}
      h1,.lede,#ctl,#meta{display:none!important}
      #stage{width:1920px!important;height:1080px!important;aspect-ratio:auto!important;border-radius:0!important}`,
  });
  await page.waitForTimeout(500);

  for (let i = 0; i < 6; i++) {
    await page.keyboard.press('Digit' + (i + 1));
    await page.waitForTimeout(2200);
    await page.screenshot({ path: `${OUT}/shot${i + 1}.png` });
    console.log(`shot${i + 1} captured`);
  }

  // Portrait: the layout-critical shots at 9:16. The viewport has to become
  // portrait too — a 1920x1920 stage inside a 1920x1080 window is simply cut.
  await page.setViewportSize({ width: 1080, height: 1920 });
  for (const n of [1, 3, 4, 6]) {
    await page.evaluate(() => document.getElementById('stage').classList.add('portrait'));
    await page.addStyleTag({ content: `#stage{width:1080px!important;height:1920px!important}` });
    await page.keyboard.press('Digit' + n);
    await page.waitForTimeout(2200);
    await page.screenshot({ path: `${OUT}/portrait${n}.png` });
    await page.evaluate(() => document.getElementById('stage').classList.remove('portrait'));
    console.log('portrait' + n + ' captured');
  }


  console.log(errors.length ? 'ERRORS:\n' + errors.join('\n') : 'no page errors');
  await browser.close();
})();
