/* 宽度/高度实测：where-overflow 报"溢出多少"，但没说"为什么"。
   这里把可疑容器的实际尺寸、滚动尺寸、以及每个 flex/grid 子项的
   基线尺寸一起打出来，才能判断是"轨道被 min-content 撑开"还是
   "固定宽度相加超过容器"。 */
'use strict';
const { chromium } = require('playwright-core');

const SRC = process.env.PLUMB_URL || 'http://127.0.0.1:8899/media/promo-class.html';
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const only = process.argv.slice(2).map(Number).filter((n) => !Number.isNaN(n));

(async () => {
  const b = await chromium.launch({ executablePath: CHROME });
  const p = await b.newPage({ viewport: { width: 1920, height: 1080 } });
  await p.goto(SRC, { waitUntil: 'networkidle' });
  // 同 check-class：先撑满舞台再量，否则量的是预览页中间态。
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
      const stage = document.getElementById('stage');
      const scs = getComputedStyle(sl);
      const box = (el) => {
        if (!el) return null;
        const r = el.getBoundingClientRect();
        const cs = getComputedStyle(el);
        return {
          cls: el.className || el.tagName.toLowerCase(),
          w: +r.width.toFixed(1), h: +r.height.toFixed(1),
          sw: el.scrollWidth, sh: el.scrollHeight,
          cw: el.clientWidth, ch: el.clientHeight,
          mt: cs.marginTop, mb: cs.marginBottom,
          pt: cs.paddingTop, pb: cs.paddingBottom,
          gtc: cs.gridTemplateColumns, gap: cs.gap,
          fs: cs.fontSize, lh: cs.lineHeight,
          pos: cs.position, disp: cs.display,
          ws: cs.whiteSpace,
        };
      };
      // 找 slide 的直接子元素里最宽/最高的
      const kids = Array.from(sl.children).map(box).filter(Boolean);
      // 收集所有带 class 的容器，找 scrollWidth > clientWidth 的
      const scrolls = [];
      for (const el of sl.querySelectorAll('*')) {
        if (el.scrollWidth > el.clientWidth + 1 || el.scrollHeight > el.clientHeight + 1) {
          const bx = box(el);
          if (bx) scrolls.push(bx);
        }
      }
      return {
        stage: box(stage),
        slidePad: scs.padding, slideDisp: scs.display,
        kids, scrolls: scrolls.slice(0, 12),
      };
    });
    const title = await p.evaluate((k) => SHOTS[k].title, i);
    const bad = r.slidePad === undefined;
    console.log(`\n=== 镜 ${i} ${title} ===`);
    console.log(`stage ${r.stage.w}x${r.stage.h}  slide padding=${r.slidePad}`);
    for (const k of r.kids) {
      const flag = k.w > r.stage.w || k.h > r.stage.h ? '  <== 超舞台' : '';
      console.log(`  子 ${k.cls.padEnd(12)} ${k.w}x${k.h} scroll=${k.sw}x${k.sh} ` +
                  `pad=${k.pt}/${k.pb} gap=${k.gap} ${k.pos}${flag}`);
      if (k.gtc && k.gtc !== 'none') console.log(`      grid 列: ${k.gtc}`);
    }
    for (const s of r.scrolls) {
      console.log(`  滚 ${s.cls.padEnd(12)} client=${s.cw}x${s.ch} scroll=${s.sw}x${s.sh} ${s.pos}`);
    }
  }

  await b.close();
})();
