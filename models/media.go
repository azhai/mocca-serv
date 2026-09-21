package models

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

// 媒体类型。
//
// 取值必须与 APP 端 `MediaKind` 逐一对齐：
// dir=0 / unknown=1 / video=2 / audio=3 / text=4 / image=5。
// 绝不能用 iota 顺排——错位会让 APP 把视频判为「未知」（不可播放）、
// 把图片判为「音频」，而且这种错位是静默的，不报错、只是行为不对。
const (
	MediaDir     = 0
	MediaUnknown = 1
	MediaVideo   = 2
	MediaAudio   = 3
	MediaText    = 4
	MediaImage   = 5
)

// 封面与缩略图统一放在数据目录下的隐藏目录：
//   - 与用户媒体目录隔离，列目录时不会把它们当成素材混进来；
//   - 点号开头在绝大多数系统与网盘里天然隐藏；
//   - 库里只存「相对数据目录」的路径，整个数据目录搬家后记录依然有效。
const (
	HiddenDirName    = ".mocca"
	CoversSubDirName = "covers"
	ThumbsSubDirName = "thumbs"
)

// CoverRelPath 封面的相对路径。
func CoverRelPath(name string) string {
	return filepath.Join(HiddenDirName, CoversSubDirName, name)
}

// ThumbRelPath 缩略图的相对路径。
func ThumbRelPath(name string) string {
	return filepath.Join(HiddenDirName, ThumbsSubDirName, name)
}

// EnsureHiddenDirs 建好隐藏目录（幂等）。
func EnsureHiddenDirs(dataDir string) error {
	for _, sub := range []string{CoversSubDirName, ThumbsSubDirName} {
		dir := filepath.Join(dataDir, HiddenDirName, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errors.Wrapf(err, "failed create %s", dir)
		}
	}
	return nil
}

// ResolveHiddenPath 把相对路径还原成绝对路径；已是绝对路径则原样返回。
func ResolveHiddenPath(dataDir, rel string) string {
	if rel == "" || filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(dataDir, rel)
}

// IsValidMediaKind 是否合法的媒体类型。
func IsValidMediaKind(kind int) bool {
	return kind == MediaVideo || kind == MediaAudio || kind == MediaImage
}
