/* 单镜 DOM 快照：把某一镜的全部后代元素连同关键计算样式打出来。
   measure-box 只看容器尺寸；这里能回答"元素在、但为什么看不见"
   （高度塌成 0 / transform 没跑 / 颜色和背景同色 / 被 overflow 裁掉）。 */
'use strict';
const { chromium } = require('playwright-core');

const SRC = process.env.PLUMB_URL || 'http://127.0.0.1:8899/media/promo-class.html';
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const shot = +(process.argv[2] || 15);
const portrait = process.argv.includes('--portrait');

(async () => {
  const b = await chromium.launch({ executablePath: CHROME });
  const vp = portrait ? { width: 1080, height: 1920 } : { width: 1920, height: 1080 };
  const p = await b.newPage({ viewport: vp });
  await p.goto(SRC, { waitUntil: 'networkidle' });
  await p.addStyleTag({
    content: `body{padding:0!important;margin:0!important}
      .wrap{max-width:none!important;padding:0!important}
      h1,.lede,#ctl,#meta{display:none!important}
      #stage{width:${vp.width}px!important;height:${vp.height}px!important;
             aspect-ratio:auto!important;border-radius:0!important;min-height:0!important}`,
  });
  if (portrait) {
    await p.evaluate(() => {
      const c = document.getElementById('por');
      c.checked = true; c.onchange({ target: c });
    });
  }
  await p.waitForTimeout(600);

  await p.evaluate((k) => show(k), shot);
  await p.waitForTimeout(3500);

  const r = await p.evaluate(() => {
    const sl = document.querySelector('.slide.on');
    const out = [];
    const walk = (el, d) => {
      const rc = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      out.push({
        d,
        tag: el.tagName.toLowerCase(),
        cls: el.className && el.className.baseVal === undefined ? el.className : '',
        w: +rc.width.toFixed(1), h: +rc.height.toFixed(1),
        x: +rc.left.toFixed(0), y: +rc.top.toFixed(0),
        fs: cs.fontSize, color: cs.color,
        bg: cs.backgroundColor, bgi: cs.backgroundImage.slice(0, 40),
        tr: cs.transform === 'none' ? '' : cs.transform,
        op: cs.opacity, vis: cs.visibility, disp: cs.display,
        anim: cs.animationName + ' ' + cs.animationDuration + ' fill:' + cs.animationFillMode,
        txt: (el.childNodes.length === 1 && el.firstChild.nodeType === 3)
          ? el.textContent.trim().slice(0, 20) : '',
      });
      for (const c of el.children) walk(c, d + 1);
    };
    walk(sl, 0);
    return out;
  });

  const title = await p.evaluate((k) => SHOTS[k].title, shot);
  console.log(`镜 ${shot} ${title}  (${portrait ? '竖版' : '横版'})`);
  for (const e of r) {
    const bad = e.w < 1 || e.h < 1 || e.op === '0' || e.vis === 'hidden';
    console.log(
      `${'  '.repeat(e.d)}${bad ? '!!' : '  '}<${e.tag}${e.cls ? ' .' + e.cls : ''}> ` +
      `${e.w}x${e.h} @${e.x},${e.y} ${e.fs} op=${e.op} ${e.disp} ` +
      (e.anim !== 'none 0s fill:none' ? `[${e.anim}] ` : '') +
      (e.tr ? `tr=${e.tr} ` : '') +
      `col=${e.color} bg=${e.bg} ${e.bgi}` +
      (e.txt ? `  "${e.txt}"` : '')
    );
  }

  await b.close();
})();
