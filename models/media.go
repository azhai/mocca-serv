package models

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// CoverRelPath 封面的相对路径（落库用）。
func CoverRelPath(name string) string {
	return filepath.Join(HiddenDirName, CoversSubDirName, name)
}

// ThumbRelPath 缩略图的相对路径（落库用）。
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

// MediaMeta 媒体元数据表。
//
// 三类媒体共有：Path（唯一锚点）、Size、Title（可空）。
// 视频/音频额外有：Duration、Cover、Description，以及多个作者（见 MediaAuthor）。
// 图片只存共有三项——刻意不落封面/时长/简介，避免表结构误导调用方。
type MediaMeta struct {
	ID          uint      `json:"id" goe:"pk"`
	Path        string    `json:"path" goe:"unique"`
	Kind        int       `json:"kind"`
	Size        int64     `json:"size"`
	Title       string    `json:"title"`
	Duration    int       `json:"duration"`                    // 毫秒；仅音视频
	Cover       string    `json:"cover"`                       // 相对路径；仅音视频
	Description string    `json:"description" goe:"type:text"` // 简介；仅音视频
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MediaAuthor 作者：一部作品可以有多个，Order 决定展示顺序。
type MediaAuthor struct {
	ID      uint   `json:"id" goe:"pk"`
	MediaID uint   `json:"media_id"`
	Name    string `json:"name"`
	Order   int    `json:"order" goe:"column:sort_order"`
}

// CreateMedia 新建元数据并写入作者列表。
func CreateMedia(m *MediaMeta, authors []string) error {
	if !IsValidMediaKind(m.Kind) {
		return errors.Errorf("invalid media kind %d", m.Kind)
	}
	if m.Path == "" {
		return errors.New("media path is empty")
	}
	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	// 图片不该带音视频专属字段，落库前清掉，避免脏数据
	if m.Kind == MediaImage {
		m.Duration, m.Cover, m.Description = 0, "", ""
	}
	if err := GetSchema().MediaMeta.Insert().One(m); err != nil {
		return errors.WithStack(err)
	}
	return SetAuthors(m.ID, authors)
}

// GetMediaByPath 按路径取元数据。
func GetMediaByPath(path string) (*MediaMeta, error) {
	m, err := GetSchema().MediaMeta.Where("path = ?", path).Select().One()
	if err != nil {
		if isNoRows(err) {
			return nil, ErrRecordNotFound
		}
		return nil, errors.Wrapf(err, "failed find media %q", path)
	}
	if m == nil {
		return nil, ErrRecordNotFound
	}
	return m, nil
}

// UpdateMedia 更新元数据。
func UpdateMedia(m *MediaMeta) error {
	if m.Kind == MediaImage {
		m.Duration, m.Cover, m.Description = 0, "", ""
	}
	m.UpdatedAt = time.Now()
	return errors.WithStack(GetSchema().MediaMeta.Save().One(m))
}

// DeleteMedia 删除元数据及其作者。
func DeleteMedia(id uint) error {
	if err := GetSchema().MediaAuthor.Delete().Where("media_id = ?", id).Exec(); err != nil {
		return errors.WithStack(err)
	}
	return errors.WithStack(GetSchema().MediaMeta.Delete().Where("id = ?", id).Exec())
}

// SetAuthors 整体替换某部作品的作者（先删后插，顺序即入参顺序）。
func SetAuthors(mediaID uint, authors []string) error {
	if err := GetSchema().MediaAuthor.Delete().Where("media_id = ?", mediaID).Exec(); err != nil {
		return errors.WithStack(err)
	}
	for i, name := range authors {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		a := &MediaAuthor{MediaID: mediaID, Name: name, Order: i}
		if err := GetSchema().MediaAuthor.Insert().One(a); err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

// ListAuthors 按展示顺序返回作者名。
func ListAuthors(mediaID uint) ([]string, error) {
	rows, err := GetSchema().MediaAuthor.Where("media_id = ?", mediaID).Select().All()
	if err != nil {
		return nil, errors.Wrap(err, "failed list authors")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Order < rows[j].Order })
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names, nil
}

// FindMediaByAuthor 按作者名反查作品（模糊匹配）。
func FindMediaByAuthor(name string) ([]*MediaMeta, error) {
	rows, err := GetSchema().MediaAuthor.Where("name = ?", name).Select().All()
	if err != nil {
		return nil, errors.Wrap(err, "failed find authors")
	}
	var out []*MediaMeta
	seen := make(map[uint]bool)
	for _, r := range rows {
		if seen[r.MediaID] {
			continue
		}
		seen[r.MediaID] = true
		m, err := GetSchema().MediaMeta.Where("id = ?", r.MediaID).Select().One()
		if err != nil {
			continue
		}
		if m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}
