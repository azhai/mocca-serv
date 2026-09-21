package mediaindex

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/azhai/mocca/drivers"
	"github.com/dhowden/tag"
	"github.com/pkg/errors"
	"github.com/rwcarlsen/goexif/exif"
)

// 封面统一尺寸与格式：缩放裁剪填满 400×300，尽量 PNG、高压缩。
const (
	CoverWidth  = 400
	CoverHeight = 300
)

// ResizeCoverPNG 把任意可解码图片按「裁剪填满」缩放到 400×300，输出高压缩 PNG。
// 供三处复用：封面上传（FsCov）、视频截图（FsShot/扫描）、音频内嵌封面（扫描）。
func ResizeCoverPNG(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.Wrap(err, "解码封面失败")
	}
	dst := coverCrop(src, CoverWidth, CoverHeight)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, dst); err != nil {
		return nil, errors.Wrap(err, "编码封面失败")
	}
	return buf.Bytes(), nil
}

// coverCrop 先等比放大到至少一边填满目标框，再居中裁剪到目标尺寸。
// 用标准库 image/draw 的矩形映射做最近邻缩放，够缩略图用（不新增 x/image 依赖）。
func coverCrop(src image.Image, w, h int) image.Image {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return src
	}
	scale := math.Max(float64(w)/float64(sw), float64(h)/float64(sh))
	nw := int(float64(sw)*scale + 0.5)
	nh := int(float64(sh)*scale + 0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	scaled := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.Draw(scaled, scaled.Bounds(), src, sb.Min, draw.Src)

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sx := (nw - w) / 2
	sy := (nh - h) / 2
	draw.Draw(dst, dst.Bounds(), scaled, image.Pt(sx, sy), draw.Src)
	return dst
}

// ImageCaptureTime 从图片 EXIF 取拍摄时间（DateTimeOriginal/DateTime），无则空串。
func ImageCaptureTime(data []byte) string {
	x, err := exif.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	t, err := x.DateTime()
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// readAudioMeta 解析音频内嵌标签，返回专辑名、作者、内嵌封面图。
// dhowden/tag 的 ReadFrom 需要 ReadSeeker（读文件头尾找标签帧），调用方需给全文。
func readAudioMeta(data []byte) (album, artist string, pic []byte, picMime string) {
	md, err := tag.ReadFrom(bytes.NewReader(data))
	if err != nil {
		return "", "", nil, ""
	}
	album, artist = md.Album(), md.Artist()
	if p := md.Picture(); p != nil && len(p.Data) > 0 {
		pic, picMime = p.Data, p.MIMEType
	}
	return album, artist, pic, picMime
}

// localRealPath 若媒体在本地磁盘，返回真实绝对路径供 ffmpeg 直读定位；非本地返回空串。
func localRealPath(drv drivers.Driver, rel string) string {
	if ld, ok := drv.(*drivers.Local); ok {
		clean := strings.TrimPrefix(rel, "/")
		if clean == "" {
			clean = "."
		}
		return filepath.Join(ld.Root, clean)
	}
	return ""
}

// FFmpegShot 调 ffmpeg 从视频里抽一帧，输出已是 400×300 封面尺寸的 PNG。
// 滤镜先整体等比缩放（lanczos，保留全画面）到接近 400×300 的覆盖尺寸，
// 再居中裁剪到精确 400×300——避免先抽全尺寸原帧、再由 Go 最近邻缩放导致的模糊。
// -ss 放在 -i 前：本地真实路径可快速定位；SMB 等远程走 stdin 流解码到目标点出帧。
func FFmpegShot(drv drivers.Driver, rel, sec string) ([]byte, error) {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", sec}
	var stdin io.Reader
	inputArg := "pipe:0"
	if lp := localRealPath(drv, rel); lp != "" {
		inputArg = lp
	} else {
		src, _, err := drv.Open(rel)
		if err != nil {
			return nil, err
		}
		var all []byte
		if buf, rerr := io.ReadAll(src); rerr == nil {
			all = buf
		}
		_ = src.(interface{ Close() error }).Close()
		stdin = bytes.NewReader(all)
	}
	args = append(args, "-i", inputArg,
		"-vf", "scale=400:300:force_original_aspect_ratio=increase:flags=lanczos,crop=400:300",
		"-frames:v", "1",
		"-f", "image2pipe", "-c:v", "png", "-")
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdin = stdin
	var out, log bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(log.String())
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(err.Error(), "executable file not found") {
			return nil, errors.New("未找到 ffmpeg，请先安装并加入 PATH")
		}
		return nil, errors.New("ffmpeg 截图失败: " + msg)
	}
	return out.Bytes(), nil
}

// WritePoster 把已编码的封面字节写入某媒体 sha1 对应的 .mocca（元数据根）海报位置。
// 目录不存在时自动创建（幂等）。供索引时自动封面与外部工具共用。
func WritePoster(drv drivers.Driver, sha1hex string, data []byte) error {
	rel, err := PosterRel(sha1hex)
	if err != nil {
		return err
	}
	dir := filepath.ToSlash(rel)
	if i := strings.LastIndexByte(dir, '/'); i > 0 {
		if err := drv.MetaMkdirAll(dir[:i]); err != nil {
			return errors.Wrapf(err, "创建 %s 失败", dir[:i])
		}
	}
	f, err := drv.MetaCreate(rel)
	if err != nil {
		return errors.Wrapf(err, "写 %s 失败", rel)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return errors.Wrapf(err, "写 %s 失败", rel)
	}
	return nil
}
