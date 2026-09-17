package models

import (
	"sort"
	"time"

	"github.com/pkg/errors"
)

// Favorite 收藏。
//
// 服务端只落这一项「跨设备需要同步」的用户数据。
// 播放历史、稍后再看、以及视频看到第几秒，都留在 APP 本地，不进库。
type Favorite struct {
	ID        uint      `json:"id" goe:"pk"`
	UserID    uint      `json:"user_id"`
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Kind      int       `json:"kind"`
	Thumb     string    `json:"thumb"` // 相对路径，见 ThumbRelPath
	CreatedAt time.Time `json:"created_at"`
}

// AddFavorite 收藏；已收藏则直接返回，不产生重复行
// （goent 只支持单列 unique，这里在应用层保证 (user_id, path) 唯一）。
func AddFavorite(f *Favorite) error {
	if f.UserID == 0 || f.Path == "" {
		return errors.New("favorite needs user_id and path")
	}
	ok, err := IsFavorited(f.UserID, f.Path)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	return errors.WithStack(GetSchema().Favorite.Insert().One(f))
}

// IsFavorited 是否已收藏。
func IsFavorited(uid uint, path string) (bool, error) {
	f, err := GetSchema().Favorite.
		Where("user_id = ? AND path = ?", uid, path).Select().One()
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "failed check favorite")
	}
	return f != nil, nil
}

// RemoveFavorite 取消收藏。
func RemoveFavorite(uid uint, path string) error {
	return errors.WithStack(
		GetSchema().Favorite.Delete().Where("user_id = ? AND path = ?", uid, path).Exec())
}

// ListFavorites 列出用户的收藏，按收藏时间倒序。
func ListFavorites(uid uint) ([]*Favorite, error) {
	rows, err := GetSchema().Favorite.Where("user_id = ?", uid).Select().All()
	if err != nil {
		return nil, errors.Wrap(err, "failed list favorites")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}
