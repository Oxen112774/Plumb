// 动画可运行性检查：静态检查（div 平衡、build 不抛错）过了不等于画面会动。
// 一条 CSS 规则被意外截断，元素就永远停在 opacity:0 —— 截图全黑，但代码"看起来没问题"。
// 这里逐镜实测入场动画后的最终不透明度与元素可见性。
// 用法：NODE_PATH=<workspace>/node_modules node check-motion.js
const { chromium } = require('playwright-core');
const path = require('path');

const SRC = process.env.PLUMB_HTML || 'E:/plumb/media/promo-motion.html';
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const SETTLE = 3200;   // 超过最长一条入场动画（760ms delay + 1200ms hero）

(async () => {
  const b = await chromium.launch({ executablePath: CHROME });
  const p = await b.newPage({ viewport: { width: 1920, height: 1080 } });
  const errs = [];
  p.on('pageerror', e => errs.push('PAGEERROR: ' + e.message));
  p.on('console', m => { if (m.type() === 'error') errs.push('CONSOLE: ' + m.text()); });

  await p.goto('file:///' + path.resolve(SRC).replace(/\\/g, '/'));
  await p.addStyleTag({ content: `
    body{padding:0;margin:0}.wrap{max-width:none;padding:0}
    header,#ctl,#prompter,#meta{display:none!important}
    #stage{width:1920px!important;height:1080px!important;min-height:0!important}
  ` });

  let bad = 0;
  for (const n of await p.evaluate(() => SHOTS.map(s => s.id))) {
    await p.evaluate(i => show(i), n);
    await p.waitForTimeout(SETTLE);
    const r = await p.evaluate(i => {
      const el = document.getElementById('sh' + i);
      // Elements carrying an entrance animation must end visible.
      //
      // Only entrance animations are checked. An infinite loop (a pulsing
      // marker, a slowly rotating ring, a label held at 72% after the
      // collapse settles) is deliberately not at full opacity — calling that
      // a failure would mean deleting the design.
      const anims = el.getAnimations({ subtree: true });
      const looping = new Set();
      for (const a of anims) {
        const t = a.effect && a.effect.getTiming ? a.effect.getTiming() : null;
        if (t && t.iterations === Infinity) {
          const target = a.effect && a.effect.target;
          if (target) looping.add(target);
        }
      }
      const animated = [...el.querySelectorAll('*')].filter(e => {
        const a = getComputedStyle(e).animationName;
        return a && a !== 'none';
      });
      // An entrance animation can also deliberately settle below 1: the
      // collapse end-card holds its six labels at 72% so the wordmark stays
      // dominant. Read the keyframes' own final opacity and compare against
      // that instead of assuming full opacity.
      const settleOf = el2 => {
        const names = getComputedStyle(el2).animationName.split(',').map(s => s.trim());
        const rules = [];
        for (const sheet of document.styleSheets) {
          let rs; try { rs = sheet.cssRules; } catch { continue; }
          for (const r of rs) if (r.type === CSSRule.KEYFRAMES_RULE) rules.push(r);
        }
        let min = 1;
        for (const r of rules) {
          if (!names.includes(r.name)) continue;
          for (const kf of r.cssRules) {
            if (kf.keyText && /(^|,)\s*100%\s*$/.test(kf.keyText)) {
              const o = kf.style.opacity;
              if (o) min = Math.min(min, parseFloat(o));
            }
          }
        }
        return min;
      };
      const stuck = animated
        .filter(e => !looping.has(e))
        .filter(e => parseFloat(getComputedStyle(e).opacity) < settleOf(e) - 0.02);
      // Also require actual pixels: something must sit inside the frame.
      const box = el.getBoundingClientRect();
      let inFrame = 0;
      for (const e of animated) {
        const q = e.getBoundingClientRect();
        if (q.width > 2 && q.height > 2 && q.right > 0 && q.left < box.width &&
            q.bottom > 0 && q.top < box.height) inFrame++;
      }
      return {
        title: SHOTS[i].title,
        animated: animated.length,
        looping: looping.size,
        stuck: stuck.map(e => e.className + ' op=' +
                    getComputedStyle(e).opacity).slice(0, 4),
        inFrame,
      };
    }, n);

    const ok = r.stuck.length === 0 && r.inFrame > 0;
    if (!ok) bad++;
    console.log(
      (ok ? '  ok  ' : ' FAIL ') +
      String(r.title).padEnd(22) +
      `animated=${String(r.animated).padStart(3)} inFrame=${String(r.inFrame).padStart(3)}` +
      (r.looping ? ` looping=${r.looping}` : '') +
      (r.stuck.length ? '\n         stuck: ' + r.stuck.join(' | ') : ''));
  }

  if (errs.length) { console.log('\n--- JS ERRORS ---'); errs.forEach(e => console.log(' ', e)); }
  console.log(bad ? `\n${bad} shot(s) have invisible elements` : '\nall shots animate in');
  await b.close();
  process.exit(bad ? 1 : 0);
})();