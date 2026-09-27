package mediaindex

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/azhai/mocca/drivers"
	"github.com/pkg/errors"
)

// 旁路（sidecar）HLS 的存放约定：服务端**不在请求时转码或切片**，只把外部工具
// 预先生成好的清单与分片当普通文件发出去（见 docs/BACKEND.md §1.3）。
//
//	<内容目录>/.hls/<视频完整文件名>/index.m3u8
//
// 目录以点开头，所以：
//   - 不会进目录列表（handlers.isExcludedName 按点开头排除）；
//   - 不会被扫描索引（scan.skipName 同样跳过点开头）；
//   - 但按显式路径取流（/d/.../.hls/a.mp4/seg0.ts）照常可用 —— 与 .mocca 封面同一条规则。
//
// 一个视频一个目录、目录名用「完整文件名」而不是去掉扩展名：否则 a.mp4 与 a.mkv
// 会映射到同一个目录，后者会静默播到前者的清单。
const (
	HLSDirName      = ".hls"       // 隐藏目录名
	HLSPlaylistName = "index.m3u8" // 清单文件名
	// HLSSegmentSeconds 分片时长（秒）。6~10 秒是 HLS 常见折中：起播与切换够快，
	// 又不至于把请求数拉得太高。
	HLSSegmentSeconds = "10"
)

// HLSPlaylistRel 由媒体在存储内的相对路径，算出旁路清单的相对路径。
//
//	movies/a.mp4 → movies/.hls/a.mp4/index.m3u8
//	a.mp4        → .hls/a.mp4/index.m3u8
//
// fileRel 为空（或以 / 结尾的目录）时返回空串。
func HLSPlaylistRel(fileRel string) string {
	clean := strings.Trim(strings.TrimSpace(fileRel), "/")
	if clean == "" {
		return ""
	}
	dir, name := "", clean
	if i := strings.LastIndexByte(clean, '/'); i >= 0 {
		dir, name = clean[:i+1], clean[i+1:]
	}
	if name == "" {
		return ""
	}
	return dir + HLSDirName + "/" + name + "/" + HLSPlaylistName
}

// HLSPlaylistPath 同上，但输入输出都是**对外路径**（带 / 与挂载前缀）。
//
//	/media/movies/a.mp4 → /media/movies/.hls/a.mp4/index.m3u8
func HLSPlaylistPath(reqPath string) string {
	rel := strings.TrimPrefix(strings.TrimSpace(reqPath), "/")
	if rel == "" {
		return ""
	}
	out := HLSPlaylistRel(rel)
	if out == "" {
		return ""
	}
	return "/" + out
}

// HLSSegment 把一个视频切成旁路 HLS：清单 + 分片写进上面约定的隐藏目录。
//
// 用 `-c copy` **只切不转**：不重编码，耗时与 CPU 都极低，画质与原文件一致。
// 代价是只有一个码率档（HLS 的自适应码率要多档 = 要转码，不在本项目范围内）。
//
// force=false 且清单已存在时直接跳过，返回 skipped=true。
// 返回的 playlistRel 是清单在存储内的相对路径（写入成功或已存在时均非空）。
//
// 本地盘：ffmpeg 直接写真实路径（最快）。SMB 等远程：先切到本机临时目录，
// 再逐个文件写回存储 —— 远程没法给 ffmpeg 一个可写的真实输出路径。
func HLSSegment(drv drivers.Driver, fileRel string, force bool) (playlistRel string, skipped bool, err error) {
	playlistRel = HLSPlaylistRel(fileRel)
	if playlistRel == "" {
		return "", false, errors.New("无法定位 HLS 清单路径")
	}
	if !force {
		if _, serr := drv.Stat(playlistRel); serr == nil {
			return playlistRel, true, nil
		}
	}

	if lp := localRealPath(drv, fileRel); lp != "" {
		outDir := filepath.Join(filepath.Dir(lp), HLSDirName, filepath.Base(lp))
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return "", false, errors.Wrapf(err, "创建 %s 失败", outDir)
		}
		if err := runFFmpegCmd("切分", hlsArgs(lp, outDir), nil); err != nil {
			return "", false, err
		}
		return playlistRel, false, nil
	}

	tmp, err := os.MkdirTemp("", "mocca-hls-")
	if err != nil {
		return "", false, errors.Wrap(err, "创建临时目录失败")
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	src, _, err := drv.Open(fileRel)
	if err != nil {
		return "", false, err
	}
	if err := runFFmpegCmd("切分", hlsArgs("pipe:0", tmp), src); err != nil {
		closeStream(src)
		return "", false, err
	}
	closeStream(src)

	if err := uploadDir(drv, tmp, path.Dir(playlistRel)); err != nil {
		return "", false, err
	}
	return playlistRel, false, nil
}

// hlsArgs 构造 ffmpeg 切分命令：输入 in，清单与分片输出到 outDir。
func hlsArgs(in, outDir string) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", in,
		"-c", "copy", // 只切不转：不重编码，画质不变、CPU 极低
		"-f", "hls",
		"-hls_time", HLSSegmentSeconds,
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"),
		filepath.Join(outDir, HLSPlaylistName),
	}
}

// runFFmpegCmd 跑一次 ffmpeg，把 stderr 收进错误信息（与 FFmpegShot 同一套口径）。
// what 只用于拼错误前缀，如「切分」「截图」。
func runFFmpegCmd(what string, args []string, stdin io.Reader) error {
	bin, err := ffmpegBin()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = stdin
	var log bytes.Buffer
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			return errors.New("未找到 ffmpeg，请先安装并加入 PATH")
		}
		if msg := strings.TrimSpace(log.String()); msg != "" {
			return errors.Errorf("ffmpeg %s失败: %s", what, msg)
		}
		return errors.Wrapf(err, "ffmpeg %s失败", what)
	}
	return nil
}

// uploadDir 把本机目录 srcDir 下的所有文件（含子目录）写回存储的 dstRel 目录。
// 逐个文件即开即关，避免分片很多时把文件描述符耗光。
func uploadDir(drv drivers.Driver, srcDir, dstRel string) error {
	root := filepath.Clean(srcDir)
	dstRel = strings.Trim(dstRel, "/")
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		dst := rel
		if dstRel != "" {
			dst = dstRel + "/" + rel
		}
		if werr := writeOneFile(drv, dst, p); werr != nil {
			return errors.Wrapf(werr, "写 %s 失败", dst)
		}
		return nil
	})
}

// writeOneFile 把本机文件 src 写到存储的 dstRel（Create 自己会建父目录）。
func writeOneFile(drv drivers.Driver, dstRel, src string) error {
	out, err := drv.Create(dstRel)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		_ = out.Close()
		return err
	}
	_, werr := io.Copy(out, in)
	_ = in.Close()
	cerr := out.Close()
	if werr != nil {
		return werr
	}
	return cerr
}
