// 本地静态服务：给宣传片的朗读/AI 功能用。
// 为什么必须有服务而不是直接开 file://——
//   1) 云服务 SDK 走 Origin 校验，file:// 的 Origin 是 "null"，模型调用会被 auth_ 拒；
//   2) file:// 下 ES module 与 fetch 的行为在 Chrome 里受限。
// 真正的 AI 对话仍需从发布域名打开（见 README），本地服务只保证朗读和画面功能可测。
// 用法：node serve.js [port]
const http = require('http');
const fs = require('fs');
const path = require('path');
const url = require('url');

const ROOT = path.resolve(__dirname, '../..');
const PORT = +(process.argv[2] || process.env.PORT || 8899);
const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png', '.jpg': 'image/jpeg', '.svg': 'image/svg+xml',
  '.txt': 'text/plain; charset=utf-8', '.md': 'text/markdown; charset=utf-8',
};

http.createServer((q, s) => {
  let p = decodeURIComponent(url.parse(q.url).pathname);
  if (p === '/') p = '/media/promo-motion.html';
  // favicon：回 204 而不是 404，否则每次检查都混进一条假报错
  if (p === '/favicon.ico') { s.writeHead(204); s.end(); return; }
  // 目录穿越防护：解析后必须仍在 ROOT 之内
  const f = path.resolve(path.join(ROOT, p));
  if (!f.startsWith(ROOT)) { s.writeHead(403); s.end('forbidden'); return; }
  fs.readFile(f, (e, d) => {
    if (e) { s.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' }); s.end('404 ' + p); return; }
    s.writeHead(200, {
      'Content-Type': MIME[path.extname(f)] || 'application/octet-stream',
      // 本地开发服务器：脚本必须每次都取新的。
      // 少了这一行，改完 voice.js 刷新页面仍跑旧代码 ——
      // 现象是"改了没生效"，排查成本极高。
      'Cache-Control': 'no-store, must-revalidate',
    });
    s.end(d);
  });
}).listen(PORT, '127.0.0.1', () => {
  console.log('plumb media server → http://127.0.0.1:' + PORT + '/media/promo-motion.html');
});
