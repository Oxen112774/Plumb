@echo off
REM ===================================================================
REM  plumb · 推送到 GitHub
REM ===================================================================
REM  用法：双击本文件。不需要打开 Git Bash。
REM
REM  会做两件事：
REM    1. 推 main 分支（用 --force-with-lease，覆盖掉建仓时 GitHub 自动生成
REM       的那个 .gitignore 提交 —— 两个历史没有共同祖先，普通 push 必被拒）
REM    2. 推送成功后打一个 v1.0.2 标签
REM
REM  凭据框里：用户名填 Oxen112774，密码填 GitHub 的 Personal Access
REM  Token（不是登录密码）。Token 在 https://github.com/settings/tokens/new
REM  生成，勾 repo 权限。
REM  停止：窗口里按 Ctrl+C
REM ===================================================================

setlocal enabledelayedexpansion
cd /d "%~dp0"

echo.
echo  plumb · 推送到 GitHub
echo  ----------------------
echo  远端：https://github.com/Oxen112774/Plumb.git
echo.

where git >nul 2>nul
if errorlevel 1 (
  echo  找不到 git。请先安装 Git for Windows:
  echo    https://git-scm.com/download/win
  echo.
  pause
  exit /b 1
)

echo  本地最近 5 个提交：
git log --oneline -5
echo.

set GIT_TERMINAL_PROMPT=0
set GITCRED=git -c credential.helper= -c credential.helper=wincred

REM --- 第一步：看远端现在有什么 -------------------------------------------
REM 建仓时勾了 Go 模板，GitHub 自己生成并提交了一个 .gitignore。那个 commit
REM 和本地 14 个提交没有共同祖先，是两条独立的根，所以普通 push 一定被拒：
REM     ! [rejected]  main -> main (fetch first)
REM     ! [rejected]  main -> main (non-fast-forward)
REM
REM 试过并确认走不通的路（别再试）：
REM   git rm origin/main:.gitignore   -> 远端是裸库，pathspec 不匹配
REM   git push origin :.gitignore      -> 裸库不支持这个 ref 删除语法
REM   checkout origin/main -> rm -> 切回本地 SHA
REM                                    -> 分支指针退回独立根，仍被拒
REM   --allow-unrelated-histories     -> 能过，但平白多一个 merge commit
REM
REM 唯一既干净又可靠的办法：--force-with-lease 覆盖掉那个模板提交。
REM 远端只有 GitHub 自己生成的一个 .gitignore，没有有价值的内容需要保留。
REM --force-with-lease 会在远端被别人改过时主动拒绝，而不是闷头覆盖。
echo正在检查远端状态...
%GITCRED% fetch origin main >nul 2>&1
for /f "%%c in ('git ls-tree --name-only -r origin/main 2^>nul ^| find /c /') do set FILECOUNT=%%c
if not defined FILECOUNT set FILECOUNT=0

if "!FILECOUNT!"=="1" (
  echo.
  echo  远端只有 1 个文件 —— 建仓时 GitHub 自动生成的 .gitignore。
  echo  本地那份覆盖更全，而且它挡住了第一次推送，将被整体替换掉。
) else (
  if "!FILECOUNT!"=="0" (
    echo  远端是空的，直接推即可。
  ) else (
    echo  远端已有 !FILECOUNT! 个文件，应该已经推过了。
  )
)

echo.
echo  正在推送（会弹 Windows 凭据框）...
echo    用户名： Oxen112774
echo    密码：   GitHub 的 Personal Access Token
echo.

%GITCRED% push --force-with-lease -u origin main
set RC=%ERRORLEVEL%

if not "%RC%"=="0" goto FAILED

REM --- 打标签：版本号与二进制一致，别人 clone 下来能直接用 -----------------
echo.
echo  正在打 v1.0.2 标签...
%GITCRED% rev-parse v1.0.2 >nul 2>&1
if errorlevel 1 (
  %GITCRED% tag -a v1.0.2 -m "内核栈归因、PSI、cgroup/K8s，以及一棵 102 叶的知识树" >nul 2>&1
)
%GITCRED% push origin v1.0.2 >nul 2>&1
if errorlevel 1 (
  echo  （标签已存在或无需更新，跳过）
) else (
  echo  标签已推送。
)

echo.
echo  ----------------------
echo  推送成功。
echo.
echo  仓库地址：https://github.com/Oxen112774/Plumb
echo.
echo  正在核对远端...
%GITCRED% fetch origin main >nul 2>&1
for /f "%%c in ('git ls-tree --name-only -r origin/main 2^>nul ^| find /c /') do set FILECOUNT=%%c
echo  远端文件数：!FILECOUNT!
echo.
echo  接下来值得做的一件事：
echo    去 Settings → About 写一句话简介，点右上角 Edit 就能改。
echo    现在那里是空的，不写的话搜不到。
goto END

:FAILED
echo.
echo  ----------------------
echo  推送失败，错误码 %RC%。
echo.
echo  常见原因：
echo.
echo  1. 凭据框里密码填错了
echo     GitHub 从 2021 年起不接受账户密码推代码，必须用 Token：
echo       https://github.com/settings/tokens/new
echo     勾 repo 权限，生成后复制那串 ghp_... （只显示一次）。
echo     用户名填 Oxen112774，密码框粘贴 Token。
echo.
echo  2. 没弹凭据框
echo     在 Git Bash 里手打这一行（手打，不要粘贴）：
echo       git -c credential.helper= push --force-with-lease -u origin main
echo.
echo  3. 报 stale info
echo     远端在你上次 fetch 之后又被改过。双击本文件重新跑一次即可。
echo.
echo  无论哪种，修好之后再双击本文件就行。

:END
echo.
pause
endlocal