#!/usr/bin/env bash
# diskcheck.sh — C 盘体检与清理建议
#
# 只读扫描，不删任何东西。先看清楚，再决定删什么。
#
#   bash diskcheck.sh          只看大小，不递归（快）
#   bash diskcheck.sh --deep   递归统计每个候选项的真实占用（慢，几分钟）

set -u

say() { printf '%s\n' "$*"; }
hr()  { say "--------------------------------------------------"; }

human() {
  local b="${1:-0}"
  if   [ "$b" -ge 1073741824 ]; then awk -v x="$b" 'BEGIN{printf "%.2f GB", x/1073741824}'
  elif [ "$b" -ge 1048576 ];    then awk -v x="$b" 'BEGIN{printf "%.1f MB", x/1048576}'
  elif [ "$b" -ge 1024 ];       then awk -v x="$b" 'BEGIN{printf "%.1f KB", x/1024}'
  else echo "$b B"; fi
}

DEEP=0
[ "${1:-}" = "--deep" ] && DEEP=1

say "=== C 盘整体 ==="
hr
df -h /c 2>/dev/null | tail -1
say ""

say "=== 常见的安装残留（装完即可删，通常是纯浪费）==="
hr

# 每一项：路径|说明|是否明确可删(y/n)
ITEMS="
C:/AMD/AMDSoftwareInstaller|AMD 驱动安装器下载包|y
C:/DrvPath|显卡驱动压缩包|y
C:/Users/Lenovo/AppData/Local/Temp/vscode-stable-user-x64|VS Code 安装包|y
C:/Users/Lenovo/AppData/Local/@genieworkbuddy-desktop-updater|WorkBuddy 更新包|y
C:/Users/Lenovo/AppData/Local/go-build|Go 编译缓存（会重建）|y
C:/Users/Lenovo/AppData/Local/CrashDumps|崩溃转储|y
C:/Users/Lenovo/AppData/Local/D3DSCache|Direct3D 着色器缓存|y
C:/Users/Lenovo/AppData/Local/NVIDIA/DXCache|NVIDIA DX 缓存|y
C:/Users/Lenovo/AppData/Local/NVIDIA/GLCache|NVIDIA GL 缓存|y
C:/Program Files (x86)/Google/GoogleUpdater/crx_cache|Chrome 扩展缓存|y
C:/Users/Lenovo/AppData/Roaming/BurpSuite/updates|BurpSuite 更新包|y
C:/Users/Lenovo/AppData/Local/CodelyTools|CodelyTools 下载的 zip|y
"

total=0
count=0
echo "$ITEMS" | while IFS='|' read -r p label safe; do
  [ -z "$p" ] && continue
  if [ ! -e "$p" ]; then continue; fi
  if [ "$DEEP" -eq 1 ]; then
    sz=$(du -sb "$p" 2>/dev/null | cut -f1)
  else
    sz=$(find "$p" -maxdepth 2 -type f -printf '%s\n' 2>/dev/null | awk '{s+=$1} END{print s+0}')
  fi
  [ "${sz:-0}" -gt 0 ] || continue
  printf '  %10s  [%s] %s\n' "$(human "$sz")" "$safe" "$label"
  printf '             %s\n' "$p"
done
say ""

if [ "$DEEP" -eq 0 ]; then
  say "  (上面是浅层统计，可能偏小。加 --deep 跑精确值，但会慢几分钟。)"
  say ""
fi

say "=== 大头在哪：几个主要目录的浅层占用 ==="
hr
for d in "C:/Users/Lenovo/AppData/Local" "C:/Users/Lenovo/AppData/Roaming" \
         "C:/Users/Lenovo/Documents" "C:/Users/Lenovo/Downloads" \
         "C:/Windows" "C:/Program Files" "C:/Program Files (x86)"; do
  [ -d "$d" ] || continue
  sz=$(find "$d" -maxdepth 3 -type f -printf '%s\n' 2>/dev/null | awk '{s+=$1} END{print s+0}')
  printf '  %10s  %s\n' "$(human "$sz")" "$d"
done
say ""

say "=== 结论 ==="
hr
avail=$(df -k /c 2>/dev/null | tail -1 | awk '{print $4}')
avail_b=$(( ${avail:-0} * 1024 ))
if [ "${avail:-0}" -lt 2097152 ]; then
  say "  可用 $(human "$avail_b") —— 不足 2 GB。"
  say ""
  say "  这个量级已经会拖垮："
  say "    · go test / go build 写临时文件失败，表现为莫名其妙的测试失败"
  say "    · git 写对象失败，push 报 unable to write"
  say "    · 编辑器的保存与备份超时"
  say ""
  say "  先删上面标 [y] 的那几项。它们都是装完就没用的东西，"
  say "  删掉不会影响任何程序的正常使用。"
  say ""
  say "  如果删完还是不够，大头在系统保护区（系统还原点 / 卷影复制），"
  say "  那个需要管理员权限，得手动处理。"
else
  say "  可用 $(human "$avail_b") —— 暂时够用。"
fi
