@echo off
REM ===================================================================
REM  plumb · 上传到 GitHub
REM ===================================================================
REM  为什么需要这个脚本：
REM    推送要 GitHub 凭据，而这个工具跑在没有交互终端的环境里 ——
REM    凭据对话框弹不出来，只能在你自己电脑上跑一次。
REM
REM  用法：双击本文件
REM  停止：窗口里按 Ctrl+C
REM ===================================================================

setlocal
cd /d "%~dp0"

echo.
echo  plumb · 推送到 GitHub
echo  ----------------------
echo  远端：https://github.com/Oxen112774/plumb.git
echo.

where git >nul 2>nul
if errorlevel 1 (
  echo  找不到 git。请先安装 Git for Windows:
  echo    https://git-scm.com/download/win
  echo.
  pause
  exit /b 1
)

echo  本地提交：
git log --oneline -5
echo.

echo  正在推送（会弹 Windows 凭据框）...
echo    用户名： Oxen112774
echo    密码：   GitHub 的 Personal Access Token（不是登录密码）
echo.

REM 用 wincred 而不是 manager。这台机器上没有安装 Git Credential
REM Manager（git-credential-manager.exe 不存在），写 manager 会直接报
REM "credential helper not found"。wincred 是 PortableGit 自带的、确实在，
REM 推送时会弹 Windows 凭据对话框。
REM 前面的空赋值是为了清掉全局那两条 helper（helper-selector + wincred
REM 显式路径）—— helper-selector 依赖一个本机不存在的选择器。
set GIT_TERMINAL_PROMPT=0
git -c credential.helper= -c credential.helper=wincred push -u origin main
set RC=%ERRORLEVEL%

echo.
if %RC% equ 0 (
  echo  ----------------------
  echo  推送成功。
  echo.
  echo  仓库地址：https://github.com/Oxen112774/plumb
) else (
  echo  ----------------------
  echo  推送失败，错误码 %RC%。
  echo.
  echo  常见原因：
  echo.
  echo  1. 仓库还不存在（最可能）
  echo     https://github.com/Oxen112774/plumb 现在是 404。
  echo     去 https://github.com/new 建一个 Oxen112774/plumb，
  echo     勾上 "Add a README file"，其他随便，然后重跑本脚本。
  echo.
  echo  2. 凭据框里密码填错了
  echo     GitHub 从 2021 年起不接受账户密码推代码，必须用 Token：
  echo       https://github.com/settings/tokens/new
  echo     勾 repo 权限，生成后复制那串 ghp_... （只显示一次）。
  echo     用户名填 Oxen112774，密码框粘贴 Token。
  echo.
  echo  3. 没弹凭据框
  echo     手动跑一次并把 Token 粘进去：
  echo       git -c credential.helper= push -u origin main
  echo.
  echo  无论哪种，修好之后再双击本文件就行。
)

echo.
pause
endlocal
