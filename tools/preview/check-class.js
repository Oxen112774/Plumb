/* 授课长版（19 镜）可运行性检查。
   动画能跑 ≠ 每一镜有画面：新镜的 build 是新写的，innerHTML 里如果
   引用了不存在的 CSS 类，元素会渲染成无样式的裸文字，肉眼能看
   到但脚本测不出来。这里逐镜实测：元素数、是否越界、有没有裸文本。 */
'use strict';
const { chromium } = require('playwright-core');
const path = require('path');

// 路径要带 /media/ —— 本仓库的预览服务以 E:/plumb 为根，
// 而打包版（剪辑2213）是以包目录为根、没有这一层。
const SRC = process.env.PLUMB_URL || 'http://127.0.0.1:8899/media/promo-class.html';
// 镜数期望做成可配，是为了让这套越界检测能原样跑到原版上取基线。
// 长版复用了原版 12 镜的 build，如果长版某镜越界而原版同名单镜也越界，
// 那是继承来的既有问题，不该算在长版头上；反过来才是新镜引入的。
const EXPECT = +(process.env.PLUMB_SHOTS || 19);
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const SETTLE = 3000;

let fails = 0;
const ok = (c, m) => { console.log((c ? '  PASS  ' : '  FAIL  ') + m); if (!c) fails++; };

(async () => {
  const b = await chromium.launch({ executablePath: CHROME });

  for (const portrait of [false, true]) {
    const vp = portrait ? { width: 1080, height: 1920 } : { width: 1920, height: 1080 };
    const p = await b.newPage({ viewport: vp });
    const errs = [];
    p.on('pageerror', (e) => errs.push(e.message));
    await p.goto(SRC, { waitUntil: 'networkidle' });
    // 量之前必须先把舞台撑满视口 —— 这一步不能省。
    // 页面里所有字号都是 vw/vh 单位（clamp(...,2.9vw,58px)），
    // 而舞台被 .wrap 的 max-width:1280px 压在 1244px 宽。vw 认的是
    // 视口 1920，不是舞台 1244，于是所有文字都比该有的尺寸大 1.5 倍，
    // 溢出是必然的 —— 但那是预览页的中间态，不是观众看到的画面。
    // 录屏和截图（shoot-shots.js）都会先把 #stage 强制成 1920x1080，
    // 检查必须跟拍摄用同一个口径，否则量的是不存在的东西。
    await p.addStyleTag({
      content: portrait
        ? `body{padding:0!important;margin:0!important}
           .wrap{max-width:none!important;padding:0!important}
           h1,.lede,#ctl,#meta{display:none!important}
           #stage{width:1080px!important;height:1920px!important;aspect-ratio:auto!important;border-radius:0!important}`
        : `body{padding:0!important;margin:0!important}
           .wrap{max-width:none!important;padding:0!important}
           h1,.lede,#ctl,#meta{display:none!important}
           #stage{width:1920px!important;height:1080px!important;aspect-ratio:auto!important;border-radius:0!important}`,
    });
    await p.waitForTimeout(600);
    if (portrait) {
      await p.evaluate(() => {
        const c = document.getElementById('por');
        c.checked = true; c.onchange({ target: c });
      });
      await p.waitForTimeout(400);
    }

    const tag = portrait ? '竖版' : '横版';
    console.log(`\n=== ${tag} ${vp.width}x${vp.height} · 19 镜逐镜实测 ===`);

    const n = await p.evaluate(() => SHOTS.length);
    ok(n === EXPECT, `镜数 = ${n}（期望 ${EXPECT}）`);

    for (let i = 0; i < n; i++) {
      await p.evaluate((k) => show(k), i);
      await p.waitForTimeout(SETTLE);
      const r = await p.evaluate(() => {
        const sl = document.querySelector('.slide.on');
        if (!sl) return { missing: true };
        const st = sl.getBoundingClientRect();
        const stage = document.getElementById('stage').getBoundingClientRect();
        // 越界：任何子元素超出舞台边界
        let over = 0, bare = 0, tiny = 0;
        // 塌陷：有内容、可见，宽或高却是 0。
        // 这条不能省 —— flex 子项的 flex-basis:0 遇上 align-items:center
        // 的父容器，宽度会算成 0，整根柱子/整张图凭空消失，
        // 而元素数、文字数、透明度全部正常，是最难发现的一类坏。
        const collapsed = [];
        for (const el of sl.querySelectorAll('*')) {
          const b = el.getBoundingClientRect();
          if (b.width === 0 && b.height === 0) continue;
          if (b.left < stage.left - 2 || b.right > stage.right + 2 ||
              b.top < stage.top - 2 || b.bottom > stage.bottom + 2) over++;
          // 可见文本但字号极小 = 样式没生效
          if (el.children.length === 0 && el.textContent.trim()) {
            const fs = parseFloat(getComputedStyle(el).fontSize);
            if (fs < 8) tiny++;
          }
          const cs = getComputedStyle(el);
          if (cs.display !== 'none' && cs.visibility !== 'hidden' &&
              parseFloat(cs.opacity) > 0.05) {
            // 纯装饰（空内容、0 字号、无背景）不算
            const deco = !el.textContent.trim() && cs.backgroundImage === 'none' &&
                         cs.backgroundColor === 'rgba(0, 0, 0, 0)' && el.children.length === 0;
            if (!deco && (b.width < 1 || b.height < 1)) {
              collapsed.push((el.className || el.tagName.toLowerCase()) +
                             `[${b.width.toFixed(1)}x${b.height.toFixed(1)}]`);
            }
          }
        }
        return {
          over, tiny, collapsed,
          els: sl.querySelectorAll('*').length,
          text: (sl.textContent || '').trim().length,
          op: +getComputedStyle(sl).opacity,
        };
      });
      const title = await p.evaluate((k) => SHOTS[k].title, i);
      if (r.missing) { ok(false, `镜 ${i} ${title}：没有 .slide.on`); continue; }
      ok(r.op > 0.9 && r.text > 10 && r.over === 0 && r.tiny === 0 && r.collapsed.length === 0,
         `镜 ${String(i).padStart(2, '0')} ${title.padEnd(14, '　')} ` +
         `元素${String(r.els).padStart(3)} 文字${String(r.text).padStart(4)} ` +
         `越界${r.over} 小字${r.tiny} 不透明${r.op.toFixed(2)}` +
         (r.collapsed.length ? ` 塌陷${r.collapsed.length}：${r.collapsed.slice(0, 3).join(' ')}` : ''));
    }

    // 提词器必须列出全部镜头
    const pr = await p.evaluate(() => ({
      lines: document.querySelectorAll('#ptLines .pline').length,
      shots: document.querySelectorAll('#ptLines .pline.shot').length,
    }));
    ok(pr.shots === EXPECT, `提词器列出 ${pr.shots} 个镜头（期望 ${EXPECT}），共 ${pr.lines} 行`);

    ok(errs.length === 0, `无 JS 报错${errs.length ? '：' + errs[0].slice(0, 70) : ''}`);
    await p.close();
  }

  await b.close();
  console.log(fails ? `\n✗ ${fails} 项失败` : '\n✓ 全部通过');
  process.exit(fails ? 1 : 0);
})();
