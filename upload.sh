#!/bin/sh
# ===================================================================
#  plumb · 上传到 GitHub（Git Bash 版）
# ===================================================================
#  用法一（在 Git Bash 里手打这一行，不要粘贴）：
#        sh /e/plumb/upload.sh
#
#  用法二：双击 E:\plumb\推送GitHub.bat
#
#  问一次用户名和密码：
#    用户名 = Oxen112774
#    密码   = Personal Access Token（不是账户密码）
#             https://github.com/settings/tokens/new 生成，勾 repo 权限
#             复制那串 ghp_...（只显示一次；粘贴时屏幕不显示字符，正常）
#
#  注意：Git Bash 终端可能开了 bracketed paste，粘贴会把不可见的
#  ESC[200~ 带进命令行，报 command not found。所以下面那行请手打。
# ===================================================================

cd /e/plumb || exit 1

echo "plumb · 推送到 GitHub"
echo "----------------------"
echo "仓库：$(git remote get-url origin)"
echo "本地提交：$(git rev-list --count HEAD) 个"
echo

# --- 代理 -----------------------------------------------------------------
# 直连 GitHub 在国内基本不通，必须走梯子。
# 这里不写死端口，而是探测本机哪个代理端口能通 GitHub —— 不同梯子的
# 端口不一样，写死了一个就换一个软件就废了。
#
# 探测方法：拿几个常见端口挨个试，看谁能返回 200。
# 找到就用，找不到就用当前的 http_proxy（可能已经配好了）。
find_proxy() {
  for p in 12000 7890 10809 7897 1080 10808 8080 8888; do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 6 \
           -x "http://127.0.0.1:$p" https://api.github.com 2>/dev/null)
    if [ "$code" = "200" ]; then
      echo "$p"
      return 0
    fi
  done
  return 1
}

if git ls-remote origin >/dev/null 2>&1; then
  echo "代理：当前设置已能连通，不改动"
else
  PROXY_PORT=$(find_proxy)
  if [ -n "$PROXY_PORT" ]; then
    export http_proxy="http://127.0.0.1:$PROXY_PORT"
    export https_proxy="$http_proxy"
    echo "代理：自动探测到 127.0.0.1:$PROXY_PORT 已设置"
  else
    echo "代理：没探测到可用端口，沿用当前设置"
    echo "  （如果推送报 Connection was reset，请确认梯子已启动）"
  fi
fi
echo

# --- 关键：为什么用 force-with-lease --------------------------------------
# 建仓时勾了 Go 模板，GitHub 自己生成了一个 .gitignore 并提交。
# 那个 commit 和本地 14 个提交没有共同祖先，是两条独立的根。
# 普通 push 一定被拒：
#     ! [rejected]  main -> main (fetch first)
#     ! [rejected]  main -> main (non-fast-forward)
#
# 试过并确认走不通的路：
#   git rm origin/main:.gitignore  → 远端是裸库，pathspec 不匹配
#   git push origin :.gitignore     → 裸库不支持这个 ref 删除语法
#   checkout origin/main → rm → checkout 回本地 SHA
#                                  → 分支指针退回独立根，仍被拒
#   --allow-unrelated-histories    → 能过，但平白多一个 merge commit
#
# 唯一既干净又可靠的办法：force-with-lease 覆盖掉那个模板提交。
# 远端只有 1 个文件（就是 GitHub 生成的 .gitignore），没有任何有价值的内容
# 需要保留，覆盖掉它是安全的；--force-with-lease 会在远端被别人改过时
# 主动拒绝，而不是闷头覆盖别人的东西。
echo "第 1 步：检查远端"
git fetch origin 2>/dev/null

REMOTE_COUNT=$(git ls-tree --name-only -r origin/main 2>/dev/null | wc -l | tr -d ' ')
echo "  远端有 ${REMOTE_COUNT:-0} 个文件"

if [ "${REMOTE_COUNT:-0}" = "1" ]; then
  echo "  → 只有 GitHub 建仓模板生成的 .gitignore，本地那份覆盖更全，替换掉它"
else
  echo "  → 直接推送"
fi
echo

# --- 推送 main -----------------------------------------------------------
echo "第 2 步：推送"
echo "  用户名：Oxen112774"
echo "  密码：  Personal Access Token（ghp_ 开头那串）"
echo
git push --force-with-lease -u origin main
RC=$?
if [ $RC -ne 0 ]; then
  echo
  echo "推送失败。常见原因："
  echo "  · 粘贴的是账户密码 —— GitHub 从 2021 年起不接受账户密码推代码，"
  echo "    必须用 Token：https://github.com/settings/tokens/new （勾 repo）"
  echo "  · 代理没通 —— 上面没探测到端口时，确认梯子已启动后重跑"
  echo "  · 报 stale info —— 远端在你 fetch 之后又被改过，重新跑一次本脚本"
  exit $RC
fi
echo

# --- 打标签 -------------------------------------------------------------
echo "第 3 步：打 v1.0.2 标签"
if git rev-parse v1.0.2 >/dev/null 2>&1; then
  echo "  （本地已存在，跳过）"
else
  git tag -a v1.0.2 -m "内核栈归因、PSI、cgroup/K8s，以及一棵 102 叶的知识树"
fi
git push origin v1.0.2 2>/dev/null && echo "  标签已推送" || echo "  标签推送跳过（多半是已存在）"
echo

# --- 核对 ---------------------------------------------------------------
echo "----------------------"
echo "推送成功"
echo
echo "仓库：https://github.com/Oxen112774/Plumb"
echo
git fetch origin 2>/dev/null
echo "远端文件数：$(git ls-tree --name-only -r origin/main | wc -l | tr -d ' ')"
echo
echo "远端前 18 个文件："
git ls-tree --name-only -r origin/main | head -18
echo
echo "还剩一件建议做的事："
echo "  Settings → About 写一句话简介（现在是空的，搜不到）"