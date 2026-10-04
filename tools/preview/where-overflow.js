/* 越界元素定位：光知道"有 4 个越界"没法动手修，得知道是哪些类、
   往哪个方向溢出、溢出多少像素。这个脚本把 check-class 的判定
   拆开打印，用法：
     PLUMB_URL=... PLUMB_SHOTS=19 node tools/preview/where-overflow.js 2 5 7 13 */
'use strict';
const { chromium } = require('playwright-core');

const SRC = process.env.PLUMB_URL || 'http://127.0.0.1:8899/media/promo-class.html';
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const only = process.argv.slice(2).map(Number).filter((n) => !Number.isNaN(n));

(async () => {
  const b = await chromium.launch({ executablePath: CHROME });
  const p = await b.newPage({ viewport: { width: 1920, height: 1080 } });
  await p.goto(SRC, { waitUntil: 'networkidle' });
  // 和 check-class 同一个口径：先把舞台撑满，否则量到的是预览页
  // 的中间态（舞台被 max-width:1280 压窄，而字号按 vw=1920 算）。
  await p.addStyleTag({
    content: `body{padding:0!important;margin:0!important}
      .wrap{max-width:none!important;padding:0!important}
      h1,.lede,#ctl,#meta{display:none!important}
      #stage{width:1920px!important;height:1080px!important;aspect-ratio:auto!important;border-radius:0!important}`,
  });
  await p.waitForTimeout(600);

  const n = await p.evaluate(() => SHOTS.length);
  const list = only.length ? only : Array.from({ length: n }, (_, i) => i);

  for (const i of list) {
    await p.evaluate((k) => show(k), i);
    await p.waitForTimeout(3000);
    const r = await p.evaluate(() => {
      const sl = document.querySelector('.slide.on');
      const st = sl.getBoundingClientRect();
      const stage = document.getElementById('stage').getBoundingClientRect();
      const out = [];
      for (const el of sl.querySelectorAll('*')) {
        const r = el.getBoundingClientRect();
        if (r.width === 0 && r.height === 0) continue;
        const d = {
          l: stage.left - r.left,
          r: r.right - stage.right,
          t: stage.top - r.top,
          b: r.bottom - stage.bottom,
        };
        const worst = Math.max(d.l, d.r, d.t, d.b);
        if (worst > 2) {
          const cs = getComputedStyle(el);
          out.push({
            tag: el.tagName.toLowerCase(),
            cls: el.className || '(无类名)',
            txt: (el.textContent || '').trim().slice(0, 24),
            left: +d.l.toFixed(1), right: +d.r.toFixed(1),
            top: +d.t.toFixed(1), bottom: +d.b.toFixed(1),
            pos: cs.position, fs: cs.fontSize,
            h: +r.height.toFixed(1), sh: sl.scrollHeight, ch: sl.clientHeight,
          });
        }
      }
      return { out, stage: { w: stage.width, h: stage.height } };
    });
    const title = await p.evaluate((k) => SHOTS[k].title, i);
    if (!r.out.length) { console.log(`镜 ${i} ${title}: 无越界`); continue; }
    console.log(`\n镜 ${i} ${title}  —— ${r.out.length} 个越界`);
    for (const o of r.out) {
      const dirs = [];
      if (o.left > 2) dirs.push(`左${o.left}`);
      if (o.right > 2) dirs.push(`右${o.right}`);
      if (o.top > 2) dirs.push(`上${o.top}`);
      if (o.bottom > 2) dirs.push(`下${o.bottom}`);
      console.log(
        `  <${o.tag} class="${o.cls}"> 溢出 ${dirs.join('/')}px  ` +
        `高${o.h} pos=${o.pos} ${o.fs}  "${o.txt}"`
      );
    }
  }

  await b.close();
})();
