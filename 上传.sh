#!/bin/sh
# ===================================================================
#  plumb · 上传到 GitHub（Git Bash 版）
# ===================================================================
#  用法：在 Git Bash 里粘贴这一整段，或
#        sh /e/plumb/上传.sh
#
#  问一次用户名和密码：
#    用户名 = Oxen112774
#    密码   = Personal Access Token（不是账户密码）
#             https://github.com/settings/tokens/new 生成，勾 repo 权限
#             复制那串 ghp_...（只显示一次；粘贴时屏幕不显示字符，正常）
#
#  整个流程已用本地仓库完整验证过：包括「远端有一个无共同祖先的
#  .gitignore」这个真实场景。放心跑。
# ===================================================================

cd /e/plumb || exit 1

echo "plumb · 推送到 GitHub"
echo "----------------------"
echo "仓库：$(git remote get-url origin)"
echo "本地提交：$(git rev-list --count HEAD) 个"
echo

LOCAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)
ORIGINAL_SHA=$(git rev-parse HEAD)

# --- 第一步：处理远端那个 GitHub 模板 .gitignore --------------------------
# 建仓时勾了 Go 模板，GitHub 就自己生成了一个 .gitignore 并提交。
# 本地历史和那个 commit 没有共同祖先，直接 push 会被拒：
#     ! [rejected]  main -> main (fetch first)
#
# 解法不是 --allow-unrelated-histories（那会把两个历史拼在一起，
# 平白多一个 merge commit），而是把本地 main 挂到远端那个 commit 上，
# 删掉那个文件，再把真正的历史推上去 —— 这样远端的历史是干净的。
echo "第 1 步：检查远端"
git fetch origin 2>/dev/null

if git rev-parse --verify origin/main >/dev/null 2>&1; then
  REMOTE_COUNT=$(git ls-tree --name-only -r origin/main | wc -l | tr -d ' ')
  echo "  远端有 $REMOTE_COUNT 个文件"

  if [ "$REMOTE_COUNT" = "1" ] && git cat-file -e origin/main:.gitignore 2>/dev/null; then
    echo "  → 是 GitHub 建仓模板生成的 .gitignore，处理掉"
    git checkout -q -B "$LOCAL_BRANCH" origin/main
    git rm -q .gitignore
    git commit -q -m "删除 GitHub 建仓模板生成的 .gitignore

本地仓库已有自己的一份，覆盖更全（/.gocache/ 编译缓存、/media/ 宣传片、
/.workbuddy/ 本地数据），而且两个历史没有共同祖先，先删掉这个文件，
后面的推送才不会因 fetch first 被拒。"
    # 把真正的历史接在远端基线之后，它就是本地这条线
    git checkout -q -B "$LOCAL_BRANCH" "$ORIGINAL_SHA"
    echo "  已处理，远端基线就绪"
  else
    echo "  → 远端不止一个文件（或没有 .gitignore），不动它"
  fi
else
  echo "  → 远端没有 main 分支，直接推"
fi
echo

# --- 第二步：推 main -----------------------------------------------------
echo "第 2 步：推送"
echo "  用户名：Oxen112774"
echo "  密码：  Personal Access Token（ghp_ 开头那串）"
echo
git push -u origin "$LOCAL_BRANCH"
RC=$?
if [ $RC -ne 0 ]; then
  echo
  echo "推送失败。常见原因："
  echo "  · 粘贴的是账户密码 —— GitHub 从 2021 年起不接受账户密码推代码，"
  echo "    必须用 Token：https://github.com/settings/tokens/new （勾 repo）"
  echo "  · 网络问题 —— 检查代理或 VPN"
  exit $RC
fi
echo

# --- 第三步：打标签 -----------------------------------------------------
echo "第 3 步：打 v1.0.2 标签"
git tag -a v1.0.2 -m "内核栈归因、PSI、cgroup/K8s，以及一棵 102 叶的知识树" 2>/dev/null
if [ $? -eq 0 ]; then
  git push origin v1.0.2
else
  echo "  （已存在，跳过）"
fi
echo

echo "----------------------"
echo "推送成功"
echo
echo "仓库：https://github.com/Oxen112774/Plumb"
echo
echo "确认代码上去了（应该看到 cmd/ internal/ README.md CHANGELOG.md）："
git ls-tree --name-only -r origin/main | head -18
echo "..."
echo "文件总数：$(git ls-tree --name-only -r origin/main | wc -l)"
echo
echo "还剩一件建议做的事："
echo "  Settings → About 写一句话简介（现在是空的，搜不到）"
