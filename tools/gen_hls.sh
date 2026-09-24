#!/usr/bin/env bash
#
# 旁路（sidecar）HLS 切分脚本 —— 与服务的存放约定完全一致：
#
#   <视频所在目录>/.hls/<视频文件名>/index.m3u8    ← 清单
#   <视频所在目录>/.hls/<视频文件名>/seg00000.ts   ← 分片
#
# 为什么是点开头的隐藏目录：既不进浏览列表，也不被扫描索引，
# 但按显式路径取流（/d/.../.hls/a.mp4/seg0.ts）照常可用。
#
# 服务端不会在请求时转码或切片；它只检测到这个清单后，把 /fs/get 的 hls_url
# 带给客户端，浏览应用便**优先走 HLS**（没有清单时仍走原来的 Range 直链）。
# 管理后台的「视频切分」页做的事也与此脚本一致（同一个约定、同一套 ffmpeg 参数）。
#
# 用法：
#   tools/gen_hls.sh a.mp4 b.mkv          # 逐个切分
#   tools/gen_hls.sh -f a.mp4             # 已存在也重切
#   tools/gen_hls.sh -t 4 a.mp4           # 分片改为 4 秒（默认 6）
#   find /media -name '*.mp4' -print0 | xargs -0 tools/gen_hls.sh
#
# 只切不转（-c copy）：画质与原文件一致、CPU 占用极低，代价是只有单码率档。

set -euo pipefail
export LC_ALL=C

SEG=6
FORCE=0

usage() {
  sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'
}

while getopts "ft:h" opt; do
  case "$opt" in
    f) FORCE=1 ;;
    t) SEG="$OPTARG" ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

command -v ffmpeg >/dev/null 2>&1 || { echo "未找到 ffmpeg，请先安装并加入 PATH" >&2; exit 1; }
if [ "$#" -eq 0 ]; then
  echo "用法: $0 [-f] [-t 秒] 视频文件..." >&2
  exit 2
fi

rc=0
for src in "$@"; do
  if [ ! -f "$src" ]; then
    echo "跳过（不是文件）: $src" >&2
    rc=1
    continue
  fi
  dir=$(dirname -- "$src")
  name=$(basename -- "$src")
  out="$dir/.hls/$name"

  if [ -f "$out/index.m3u8" ] && [ "$FORCE" -eq 0 ]; then
    echo "已存在，跳过: $out/index.m3u8（加 -f 可重切）"
    continue
  fi

  mkdir -p "$out"
  echo "切分: $src → $out/index.m3u8"
  if ffmpeg -hide_banner -loglevel error -y -i "$src" \
      -c copy -f hls \
      -hls_time "$SEG" \
      -hls_playlist_type vod \
      -hls_segment_filename "$out/seg%05d.ts" \
      "$out/index.m3u8"; then
    echo "  完成：$(find "$out" -maxdepth 1 -type f | wc -l | tr -d ' ') 个文件"
  else
    echo "  失败: $src" >&2
    rc=1
  fi
done

exit $rc
