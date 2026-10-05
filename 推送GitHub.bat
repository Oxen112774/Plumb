@echo off
REM ===================================================================
REM  plumb · 上传到 GitHub
REM ===================================================================
REM  这个脚本会做三件事：
REM    1. 如果远端只有 GitHub 自动生成的 .gitignore，先删掉它
REM       （两个历史没有共同祖先，带着它推会被拒；而且它不如本地那份全）
REM    2. 推 main 分支
REM    3. 推送成功后打一个 v1.0.2 标签
REM
REM  用法：双击本文件。凭据框里用户名填 Oxen112774，密码填 GitHub 的
REM  Personal Access Token（不是登录密码）。
REM  Token 在 https://github.com/settings/tokens/new 生成，勾 repo 权限。
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

echo  本地提交：
git log --oneline -5
echo.

set GIT_TERMINAL_PROMPT=0
set GITCRED=git -c credential.helper= -c credential.helper=wincred

REM --- 第一步：远端只有 GitHub 自动生成的 .gitignore 时先清掉 -------------
REM 远端那个文件是建仓时选 Go 模板生成的。它有两个问题：一是两个历史没有
REM 共同祖先，带着它推会被拒（fetch first）；二是它的覆盖面不如本地那份
REM （缺 /.gocache/、/media/ 这些真正占地方的），推上去会覆盖掉。
REM 判断方式是远端内容只有一个 .gitignore，且本地已有自己的版本。
echo  正在检查远端状态...
%GITCRED% ls-remote --exit-code -h origin >nul 2>&1
set REMOTE_EXISTS=!errorlevel!

if not "!REMOTE_EXISTS!"=="0" (
  echo.
  echo  远端是空的，直接推即可。跳过清理。
) else (
  %GITCRED% fetch origin main >nul 2>&1
  for /f "%%c in ('git ls-tree --name-only -r origin/main 2^>nul ^| find /c /') do set FILECOUNT=%%c
  if not defined FILECOUNT set FILECOUNT=0

  if "!FILECOUNT!"=="1" (
    echo.
    echo  远端只有一个文件 —— 应该是建仓时 GitHub 生成的 .gitignore。
    echo  它会让第一次推送被拒（两个历史无共同祖先），而且覆盖面不如本地那份。
    echo  正在删除远端这个文件...
    %GITCRED% rm origin/main:.gitignore >nul 2>&1
    if errorlevel 1 (
      echo  [!] 删除失败。如果这一步报错，改为手动处理：
      echo      打开 https://github.com/Oxen112774/Plumb
      echo      点 .gitignore → 右上角垃圾桶 → 底部直接提交
      echo      然后再双击本文件。
    ) else (
      echo  已删除。继续。
    )
  ) else (
    echo  远端已有 !FILECOUNT! 个文件，看起来已经推过了，直接推送。
  )
)

echo.
echo  正在推送（会弹 Windows 凭据框）...
echo    用户名： Oxen112774
echo    密码：   GitHub 的 Personal Access Token
echo.

%GITCRED% push -u origin main
set RC=%ERRORLEVEL%

if not "%RC%"=="0" goto FAILED

REM --- 打标签：版本号与二进制一致，别人 clone 下来能直接用 -----------------
for /f "tokens=2" %%v in ('git describe --tags --abbrev=0 2^>nul') do set LASTTAG=%%v
if not defined LASTTAG set LASTTAG=none
echo.
echo  当前最后一个标签：!LASTTAG!
%GITCRED% tag -a v1.0.2 -m "内核栈归因、PSI、cgroup/K8s，以及一棵 102 叶的知识树" 2>nul
if errorlevel 1 (
  echo  （标签已存在，跳过）
)
%GITCRED% push origin v1.0.2 >nul 2>&1

echo.
echo  ----------------------
echo  推送成功。
echo.
echo  仓库地址：https://github.com/Oxen112774/Plumb
echo.
echo  接下来值得做的一件事：
echo    去 Settings → About 写一句话简介，点右上角 Edit 就能改。
echo    现在那里是空的。
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
echo     手动跑一次并把 Token 粘进去：
echo       git -c credential.helper= push -u origin main
echo.
echo  3. 报 fetch first / non-fast-forward
echo     远端还有东西没清掉。回上面的第 1 步手动删掉 GitHub 生成的
echo     .gitignore，再推一次。
echo.
echo  无论哪种，修好之后再双击本文件就行。

:END
echo.
pause
endlocal
