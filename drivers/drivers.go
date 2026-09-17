// Package drivers 媒体源抽象：目前只做读取（列目录 + 开文件）。
// 新增一种存储 = 实现 Driver 接口，handler 不用改。
package drivers

import (
	"io"
	"strings"
	"time"

	"github.com/azhai/mocca/models"
	"github.com/pkg/errors"
)

// Entry 目录条目，与具体存储无关。
type Entry struct {
	Name     string
	Size     int64
	IsDir    bool
	Modified time.Time
}

// Driver 媒体源。
//
// Open 必须返回可 Seek 的流：播放器拖动进度条依赖 Range 请求，
// 而 Range 需要知道文件大小并能任意定位。
type Driver interface {
	List(rel string) ([]Entry, error)
	// Stat 取单个条目的信息但不打开内容，用于「取详情」这类只读元信息的场景。
	Stat(rel string) (Entry, error)
	Open(rel string) (io.ReadSeeker, int64, error)
	// 写入能力：上传用。Create 自己负责把父目录建出来。
	MkdirAll(rel string) error
	Create(rel string) (io.WriteCloser, error)
	// Remove 删除文件或整个目录（递归）。
	Remove(rel string) error
	// Rename 在同一目录内改名。
	Rename(rel, newName string) error
	// Move 移动到另一个目录（目标目录相对同一存储根）。
	Move(rel, dstDirRel string) error
	Close() error
}

// 驱动名（不区分大小写）。
const (
	DriverLocal = "local"
	DriverSMB   = "smb"
)

// Open 按存储的 Driver 字段建一个可用的媒体源。
func Open(s *models.Storage) (Driver, error) {
	switch strings.ToLower(strings.TrimSpace(s.Driver)) {
	case "", DriverLocal:
		return NewLocal(s)
	case DriverSMB, "samba":
		return NewSMB(s)
	default:
		return nil, errors.Errorf("unsupported driver %q", s.Driver)
	}
}

// relPath 把对外相对路径规范成存储内的相对路径，并挡住 ../ 越权。
func relPath(rel string) string {
	clean := filepathCleanSlash(rel)
	if clean == "/" {
		return "."
	}
	return strings.TrimPrefix(clean, "/")
}
