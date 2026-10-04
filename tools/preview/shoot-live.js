// 给新功能截图：浮层 + 朗读高亮 + 竖版。
// 已有 26 张逐镜预览图（preview/），这里补的是"新功能长什么样"的证据。
// 用法：NODE_PATH=<workspace>/node_modules node shoot-live.js
const { chromium } = require('playwright-core');
const path = require('path');
const fs = require('fs');

const URL = process.env.PLUMB_URL || 'http://127.0.0.1:8899/media/promo-motion.html';
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const OUT = path.resolve(__dirname, '../../media/preview');

(async () => {
  const b = await chromium.launch({ executablePath: CHROME });

  for (const [tag, w, h, portrait] of [['live', 1920, 1080, false], ['live-portrait', 1080, 1920, true]]) {
    const p = await b.newPage({ viewport: { width: w, height: h } });
    await p.goto(URL, { waitUntil: 'networkidle' });
    await p.waitForTimeout(600);
    await p.evaluate(({ w, h, portrait }) => {
      // 只截舞台：录屏时录的就是这一块
      const s = document.createElement('style');
      s.textContent = `.wrap{max-width:none;padding:0}header,#ctl,#askRow,#prompter,#meta{display:none!important}
        #stage{width:${w}px!important;height:${h}px!important;min-height:0!important;border-radius:0}`;
      document.head.appendChild(s);
      if (portrait) { const c = document.getElementById('por'); c.checked = true; c.onchange({ target: c }); }
    }, { w, h, portrait });

    // 1) 完整应答态：verdict 与 say 互斥，同屏只出现 verdict
    await p.evaluate(() => {
      show(10);
      showLive(document.getElementById('lvHeard'), 'K8s 里 CPU 才 8% 为什么还会被限流', 60000);
      showLive(document.getElementById('lvVerdict'), '限流看配额，不看均值', 60000);
    });
    await p.waitForTimeout(1600);
    const f1 = path.join(OUT, `${tag}-overlay.png`);
    await p.locator('#stage').screenshot({ path: f1 });
    console.log('→', path.relative(process.cwd(), f1));

    // 2) 语音识别中：画面进入 listening 呼吸态，只有 heard 一层
    await p.evaluate(() => {
      document.querySelectorAll('.lv').forEach(e => e.classList.remove('show'));
      showLive(document.getElementById('lvHeard'), '我这个容器一直在被限流', 60000);
      document.getElementById('mic').classList.add('on');
      document.getElementById('micDot').classList.add('live');
      document.getElementById('stage').classList.add('listening');
    });
    await p.waitForTimeout(1700);   // 停在呼吸动画的中段，截图才看得出效果
    const f2 = path.join(OUT, `${tag}-listening.png`);
    await p.locator('#stage').screenshot({ path: f2 });
    console.log('→', path.relative(process.cwd(), f2));

    // 3) AI 口头解释态（无 verdict 时才显示 say）
    await p.evaluate(() => {
      document.getElementById('stage').classList.remove('listening');
      document.getElementById('mic').classList.remove('on');
      document.getElementById('micDot').classList.remove('live');
      document.querySelectorAll('.lv').forEach(e => e.classList.remove('show'));
      showLive(document.getElementById('lvSay'), '别被平均值骗了，限流看的是 cgroup 的 CPU 配额。', 60000);
    });
    await p.waitForTimeout(900);
    const f3 = path.join(OUT, `${tag}-reply.png`);
    await p.locator('#stage').screenshot({ path: f3 });
    console.log('→', path.relative(process.cwd(), f3));

    await p.close();
  }
  await b.close();
  console.log('done');
})();
