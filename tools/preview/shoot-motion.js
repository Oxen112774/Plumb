// 无头截图：把 media/promo-motion.html 的每一镜拍成 1080p 静帧。
// 图形动画的 bug 静态检查抓不到（div 平衡过了、build 逻辑过了、
// clamp 写了，照样错），所以必须真看。
//
// 用系统已装的 Chrome，不必下载 Chromium。
// 用法：NODE_PATH=<workspace>/node_modules node shoot-motion.js [--portrait]
const { chromium } = require('playwright-core');
const path = require('path');
const fs = require('fs');

const SRC = process.env.PLUMB_HTML || 'E:/plumb/media/promo-motion.html';
const OUT = process.env.PLUMB_OUT || 'E:/plumb/media/.shots';
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const PORTRAIT = process.argv.includes('--portrait');

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const b = await chromium.launch({ executablePath: CHROME });
  const vp = PORTRAIT ? { width: 1080, height: 1920 } : { width: 1920, height: 1080 };
  const p = await b.newPage({ viewport: vp });

  const errs = [];
  p.on('pageerror', e => errs.push('PAGEERROR: ' + e.message));
  p.on('console', m => { if (m.type() === 'error') errs.push('CONSOLE: ' + m.text()); });

  await p.goto('file:///' + path.resolve(SRC).replace(/\\/g, '/'));

  // Hide the page chrome and pin the stage to the delivery size, otherwise the
  // clamp() values resolve against the window instead of the film frame.
  await p.addStyleTag({ content: `
    body{padding:0;margin:0}
    .wrap{max-width:none;padding:0}
    header,#ctl,#prompter,#meta{display:none!important}
    #stage{width:${vp.width}px!important;height:${vp.height}px!important;min-height:0!important}
  `});
  if (PORTRAIT) {
    await p.evaluate(() => document.getElementById('stage').classList.add('portrait'));
  }

  const shots = await p.evaluate(() => SHOTS.map(s => s.id));
  const prefix = PORTRAIT ? 'p' : 's';

  for (const n of shots) {
    await p.evaluate(i => show(i), n);
    await p.waitForTimeout(2400);   // let entrance animations finish
    const f = `${OUT}/${prefix}${String(n).padStart(2, '0')}.png`;
    await p.screenshot({ path: f });
    console.log(path.basename(f), 'captured');
  }

  if (errs.length) { console.log('\n--- JS ERRORS ---'); errs.forEach(e => console.log(' ', e)); }
  else console.log('\nno JS errors');
  await b.close();
})();