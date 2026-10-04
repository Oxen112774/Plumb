// 朗读 / AI 导演功能检查。
// 动画能跑 ≠ 新功能能用：voice.js 语法错、PlumbVoice 没挂上、
// 浮层被 z-index 压住、提词器高亮不跟随 —— 这些都不会让 check-motion 报错。
// 这里逐项实测。
// 用法：NODE_PATH=<workspace>/node_modules node check-voice.js
const { chromium } = require('playwright-core');

// 走 http 而不是 file://：云服务 SDK 有 Origin 校验，file:// 下 Origin 是 null，
// 模型调用会被鉴权拒，测出来的"AI 失败"是假阳性。
// 跑之前先双击 启动放映.bat（或 node 启动服务.js）。
const URL = process.env.PLUMB_URL || 'http://127.0.0.1:8899/promo-motion.html';
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';

let fails = 0;
const ok = (c, m) => { console.log((c ? '  PASS  ' : '  FAIL  ') + m); if (!c) fails++; };

(async () => {
  const b = await chromium.launch({ executablePath: CHROME });
  const p = await b.newPage({ viewport: { width: 1600, height: 1000 } });
  const errs = [];
  p.on('pageerror', e => errs.push('PAGEERROR: ' + e.message));
  p.on('console', m => { if (m.type() === 'error') errs.push('CONSOLE: ' + m.text()); });

  await p.goto(URL, { waitUntil: 'networkidle' });
  await p.waitForTimeout(600);

  console.log('\n=== 1. 模块加载 ===');
  const mods = await p.evaluate(() => ({
    voice: typeof window.PlumbVoice,
    director: typeof window.PlumbDirector,
    sdk: typeof window.WorkBuddyCloud,
    voiceSupported: window.PlumbVoice ? PlumbVoice.supported : null,
    srSupported: window.PlumbDirector ? PlumbDirector.srSupported : null,
    terms: window.PlumbVoice ? PlumbVoice.terms.length : 0,
  }));
  ok(mods.voice === 'object', 'voice.js 已挂载 PlumbVoice');
  ok(mods.director === 'object', 'director.js 已挂载 PlumbDirector');
  ok(mods.sdk === 'object', '云服务 SDK 已加载（' + mods.sdk + '）');
  ok(mods.terms > 50, '术语表 ' + mods.terms + ' 条');
  console.log('        语音合成支持: ' + mods.voiceSupported + ' / 语音识别支持: ' + mods.srSupported);

  console.log('\n=== 2. 中英切段（这是"我不会读英语"的解法） ===');
  const segs = await p.evaluate(() => {
    // 逐段可读地打印：pause 段没有 text，直接取会得到 undefined
    const one = t => PlumbVoice.compileLine(t, { pauseMs: 800 }).map(s =>
      s.kind === 'pause' ? 'pause@' + s.ms
        : s.kind + '(' + s.lang + '):' + s.text);
    return {
      psi:    one('内核里有个专门的量，叫 PSI —— 压力 stalled information。'),
      cgroup: one('这台机器的 cgroup v2 里 CPU 被限流了 80%。'),
      pause:  PlumbVoice.compileLine('（停顿）', { pauseMs: 800 }).map(s => s.kind + ':' + s.ms),
      // 单独看一条纯中文句，确认 lang 判定本身是对的
      zhOnly: PlumbVoice.compileLine('它统计的不是用了多少。', {}).map(s => s.lang),
    };
  });
  console.log('        PSI 句 → ' + segs.psi.join(' '));
  console.log('        cgroup 句 → ' + segs.cgroup.join(' '));
  // PSI 必须真的拆成 3 个音节 + 2 个 90ms 间隙，而不是整块丢给 TTS
  ok(segs.psi.includes('word(en):P') && segs.psi.includes('word(en):S') && segs.psi.includes('word(en):I'),
     'PSI 拆成 3 个独立音节 P / S / I');
  ok(segs.psi.filter(s => s === 'pause@90').length === 2, '音节之间有 2 个 90ms 间隙');
  ok(segs.zhOnly.every(l => l === 'zh'), '纯中文句全部判为 zh 音色（实测 ' + JSON.stringify(segs.zhOnly) + '）');
  ok(segs.psi.includes('word(en):stalled information'), '英文词组保持完整');
  ok(segs.cgroup.includes('word(en):C') && segs.cgroup.includes('word(en):group'), 'cgroup 拆成 C / group');
  ok(segs.cgroup.some(s => s.includes('80')), '数字 80% 归到英文段');
  ok(segs.pause[0] === 'pause:800', '（停顿）编译成 pause 片段');

  console.log('\n=== 3. 朗读按钮 + 提词器高亮跟随 ===');
  // 无头 Chrome 没有真实语音合成设备，speakOne 会走估时分支 ——
  // 正好能验证"高亮是否跟着朗读推进"这条链路。
  await p.evaluate(() => { document.getElementById('tts').click(); });
  await p.waitForTimeout(700);
  const mid = await p.evaluate(() => {
    const cur = document.querySelector('#ptLines .pline.cur');
    return {
      speaking: PlumbVoice.isSpeaking(),
      curText: cur ? cur.textContent.slice(0, 30) : null,
      btnOn: document.getElementById('tts').classList.contains('on'),
      hint: document.getElementById('ptHint').textContent,
    };
  });
  ok(mid.speaking, '朗读已启动');
  ok(!!mid.curText, '提词器有当前句高亮：' + JSON.stringify(mid.curText));
  ok(mid.btnOn, '朗读按钮进入 on 状态');
  ok(/正在朗读/.test(mid.hint), '提词器提示已切换为朗读态');
  // 实测时序：第 0 句 ~1.5s 触发，第 1 句 ~4.6s 触发。
  // 到 3.9s（700+3200）还停在第 0 句是**正确**的跟读节奏，不是 bug。
  // 要看到推进必须越过 4.6s。
  await p.waitForTimeout(4600);
  const later = await p.evaluate(() => {
    const cur = document.querySelector('#ptLines .pline.cur');
    return cur ? cur.textContent.slice(0, 30) : null;
  });
  ok(later !== mid.curText, '高亮已推进到下一句：' + JSON.stringify(later));
  // 节奏必须由我们定：整镜不能几秒念完，否则观众跟不上
  const stillPlaying = await p.evaluate(() => PlumbVoice.isSpeaking());
  ok(stillPlaying, '朗读仍在进行（节奏跟着人走，不跟着声码器快慢走）');
  // 已念过的句子要留痕，方便跟读时确认自己漏了哪句
  const done = await p.evaluate(() => document.querySelectorAll('#ptLines .pline.done').length);
  ok(done > 0, '已念句标记为 done（' + done + ' 句）');

  console.log('\n=== 4. 浮层：实时上屏且不被遮挡 ===');
  // 只验"浮层出没出来"，不发真实 AI 请求 ——
  // 请求会飞几十秒，后面的检查项全被它占着（并发问题会伪装成
  // "localShot 不生效"，很难查）。直接调页面的 showLive 路径。
  const ov = await p.evaluate(() => {
    showLive(document.getElementById('lvHeard'), '为什么我的指标都是绿的但服务还是慢', 9000);
    const wrap = document.getElementById('live');
    const h = document.getElementById('lvHeard');
    const r = h.getBoundingClientRect();
    const st = getComputedStyle(wrap);
    return {
      shown: h.classList.contains('show'),
      text: h.querySelector('.txt').textContent,
      opacity: +getComputedStyle(h).opacity,
      z: parseInt(st.zIndex, 10),
      top: Math.round(r.top), hgt: Math.round(r.height),
      capTop: Math.round(document.getElementById('cap').getBoundingClientRect().top),
    };
  });
  ok(ov.shown && ov.opacity > 0.9, '浮层已显示（opacity ' + ov.opacity + '）');
  ok(ov.text === '为什么我的指标都是绿的但服务还是慢', '浮层显示你的原话');
  ok(ov.z >= 40, '#live z-index ' + ov.z + ' ≥ 40，在画面之上');
  ok(ov.top + ov.hgt < ov.capTop, '浮层底边 ' + (ov.top + ov.hgt) + ' < 字幕顶边 ' + ov.capTop + '，不遮字幕');
  // 三个形态都要能显示，但 verdict 与 say 互斥（同屏两层会遮主视觉，
  // 截图实测过）。所以依次验证，而不是一次性全开。
  const allThree = await p.evaluate(() => {
    const out = {};
    for (const id of ['lvHeard', 'lvSay', 'lvVerdict']) {
      document.querySelectorAll('.lv').forEach(e => e.classList.remove('show'));
      showLive(document.getElementById(id), '测试文本', 3000);
      out[id] = document.getElementById(id).classList.contains('show');
    }
    // 互斥：开 verdict 时 say 必须收起
    showLive(document.getElementById('lvVerdict'), '结论', 3000);
    out.verdictHidesSay = !document.getElementById('lvSay').classList.contains('show');
    showLive(document.getElementById('lvSay'), '回答', 3000);
    out.sayHidesVerdict = !document.getElementById('lvVerdict').classList.contains('show');
    // 同时可见的层数不得超过 2
    showLive(document.getElementById('lvHeard'), '你说', 3000);
    showLive(document.getElementById('lvVerdict'), '结论', 3000);
    out.maxTwo = [...document.querySelectorAll('.lv.show')].length <= 2;
    return out;
  });
  ok(allThree.lvHeard && allThree.lvSay && allThree.lvVerdict,
     '三种浮层形态都能显示：' + JSON.stringify(allThree));
  ok(allThree.verdictHidesSay && allThree.sayHidesVerdict,
     '结论与回答互斥，不会叠在一起');
  ok(allThree.maxTwo, '同屏最多 2 层，不遮主视觉');

  // 「在听」时画面必须真的动起来，而不只是多一行字
  const dyn = await p.evaluate(async () => {
    document.getElementById('stage').classList.add('listening');
    await new Promise(r => setTimeout(r, 400));
    const a = getComputedStyle(document.querySelector('.slide.on')).transform;
    await new Promise(r => setTimeout(r, 1500));
    const b = getComputedStyle(document.querySelector('.slide.on')).transform;
    const glow = getComputedStyle(document.getElementById('stage'), '::after').animationName;
    document.getElementById('stage').classList.remove('listening');
    return { a, b, glow };
  });
  ok(dyn.a !== dyn.b, '说话时主视觉在动（transform 随时间变化：' + dyn.a + ' → ' + dyn.b + '）');
  ok(dyn.glow === 'breatheGlow', '画面光晕呼吸动画已挂上：' + dyn.glow);

  console.log('\n=== 5. 本地关键词兜底（不依赖 AI，模型归错时由它裁决） ===');
  const kw = await p.evaluate(() => {
    const cases = [
      ['K8s 里 CPU 才 8% 为什么还会被限流', 10],
      ['我这个容器 pod 一直在被 throttle', 10],
      ['进程卡住了 kill 不掉，全是 D 状态', 3],
      ['脏页太多写太快盘跟不上', 7],
      ['网络抖动很厉害，丢包和重传都高', 9],
      ['free 显示还有内存却分配失败', 8],
      ['CPU 总用量不高但某个核跑满了', 6],
      ['怎么知道卡在哪一步，看内核栈吗', 4],
      ['PSI 压力是什么意思', 2],
      ['plumb 怎么安装', 12],
      ['今天天气不错', null],
    ];
    return cases.map(([q, want]) => ({ q, want, got: PlumbDirector.localShot(q) }));
  });
  for (const c of kw) {
    ok(c.got === c.want, '「' + c.q + '」→ 镜 ' + c.got + (c.want === null ? '（不命中，保持当前）' : '（期望 ' + c.want + '）'));
  }

  console.log('\n=== 6. AI 应答链路（真实调用免密钥模型） ===');
  // 先停朗读，否则 TTS 队列和模型请求抢主线程
  await p.evaluate(() => document.getElementById('ttsStop').click());
  const ai = await p.evaluate(async () => {
    const t0 = Date.now();
    return await new Promise(res => {
      // 免密钥模型冷启动实测 85-115s，给 240s 上限，超时按失败计
      const to = setTimeout(() => res({ err: '240s 超时，没有应答' }), 240000);
      // on() 是追加语义，不会顶掉页面自己的切镜/浮层回调
      PlumbDirector.on('onAnswer', (cmd, heard) => { clearTimeout(to); res({ cmd, heard, ms: Date.now() - t0 }); });
      PlumbDirector.on('onError', m => { clearTimeout(to); res({ err: m, ms: Date.now() - t0 }); });
      PlumbDirector.askText('K8s 里 CPU 才 8% 为什么还会被限流');
    });
  });
  if (ai.err) {
    ok(false, 'AI 调用失败：' + ai.err);
  } else {
    console.log('        AI 说：' + ai.cmd.reply);
    console.log('        指令：shot=' + ai.cmd.shot + ' overlay=' + ai.cmd.overlay
      + ' overlayText=' + JSON.stringify(ai.cmd.overlayText)
      + ' terms=' + JSON.stringify(ai.cmd.terms) + '  耗时 ' + (ai.ms/1000).toFixed(1) + 's');
    ok(typeof ai.cmd.reply === 'string' && ai.cmd.reply.length > 4, 'AI 返回了台词');
    ok(ai.cmd.shot === null || (ai.cmd.shot >= 0 && ai.cmd.shot <= 12), 'shot 在 0-12 范围内或为 null');
    ok(['none', 'heard', 'verdict'].includes(ai.cmd.overlay), 'overlay 是合法枚举');
    ok(Array.isArray(ai.cmd.terms), 'terms 是数组');
    // 问 K8s 限流就该切到第 10 镜（K8s），这是"画面跟着说话变"的核心
    ok(ai.cmd.shot === 10, '问 K8s 限流 → 切到第 10 镜（实际 ' + ai.cmd.shot + '）');
    // 画面必须真的切过去了
    const nowAt = await p.evaluate(() => cur);
    ok(nowAt === 10, '画面已切到第 ' + nowAt + ' 镜（K8s）');
    // AI 的话要上屏。
    // 注意：结论层与回答层是**互斥**的（同屏最多两层，否则会压住主视觉），
    // 所以 AI 返回 overlay=verdict 时，看到的是结论而不是回答。
    // 这里按 overlay 分派去查对应那一层，别写死查 lvSay ——
    // 那样模型一旦回 verdict 就会误报失败（曾经真的误报过）。
    const say = await p.evaluate((ov) => {
      const read = (id) => {
        const n = document.getElementById(id);
        return { shown: n.classList.contains('show'), t: n.querySelector('.txt').textContent };
      };
      return { want: ov, verdict: read('lvVerdict'), say: read('lvSay') };
    }, ai.cmd.overlay);
    if (say.want === 'verdict') {
      ok(say.verdict.shown && say.verdict.t.length > 0,
         'AI 结论已上屏：' + JSON.stringify(say.verdict.t));
      ok(!say.say.shown, '结论与回答互斥，回答层让位（不遮主视觉）');
    } else {
      ok(say.say.shown && say.say.t.length > 0,
         'AI 回答已上屏：' + JSON.stringify(say.say.t.slice(0, 20)));
    }
  }

  console.log('\n=== 7. 提问栏接线（放在真 AI 检查之后） ===');
  const wire = await p.evaluate(() => {
    const inp = document.getElementById('askIn');
    inp.value = '脏页是什么';
    document.getElementById('askGo').click();
    const st = document.getElementById('askStat').textContent;
    const cleared = inp.value === '';
    // 这条请求会飞几十秒，按了停止把队列清掉
    PlumbDirector.abort();
    PlumbDirector.flushQueue();
    return { stat: st, cleared };
  });
  ok(/思考中/.test(wire.stat), '点「问 AI」后状态栏提示：' + JSON.stringify(wire.stat));
  ok(wire.cleared, '输入框已清空');

  console.log('\n=== 8. 模型输出不可信时的兜底 ===');
  const guard = await p.evaluate(() => {
    show(10);
    return { before: cur, slides: document.querySelectorAll('.slide').length };
  });
  ok(guard.before === 10, '可直接切到第 10 镜（K8s）');
  ok(guard.slides <= 13, '幻灯片按需创建，共 ' + guard.slides + ' 张');

  console.log('\n=== 9. 页面无 JS 报错 ===');
  const real = errs.filter(e => !/favicon|ERR_INTERNET|cdn\.jsdelivr.*Failed/i.test(e));
  ok(real.length === 0, real.length ? real.join('\n') : '无报错');

  await b.close();
  console.log('\n' + (fails ? '✗ ' + fails + ' 项失败' : '✓ 全部通过'));
  process.exit(fails ? 1 : 0);
})();
