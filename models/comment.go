package models

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pkg/errors"
)

// 评论与弹幕共用一张表，用 Type 区分：
//   - 评论：不限字数，Offset 恒为 0（挂在整部作品上，不挂在某个时刻）；
//   - 弹幕：限 MaxDanmakuLength 字，Offset 为它在视频里出现的时刻（毫秒）。
const (
	CommentTypeComment = iota
	CommentTypeDanmaku
)

// MaxDanmakuLength 弹幕字数上限。按 Unicode 码点计，
// 否则中文按字节算会「三个 byte 才算一个字」，把 17 个汉字误判成 51 字。
const MaxDanmakuLength = 50

// Comment 评论 / 弹幕。
type Comment struct {
	ID        uint      `json:"id" goe:"pk"`
	UserID    uint      `json:"user_id"`
	Path      string    `json:"path"`   // 关联的媒体路径（元数据可缺省，故用路径做锚点）
	Type      int       `json:"type"`   // 0 评论 / 1 弹幕
	Offset    int       `json:"offset"` // 毫秒；弹幕为出现时刻，评论恒为 0
	Content   string    `json:"content" goe:"type:text"`
	CreatedAt time.Time `json:"created_at"`
}

// IsDanmaku 是否弹幕。
func (c *Comment) IsDanmaku() bool { return c.Type == CommentTypeDanmaku }

// Validate 落库前校验，并顺手把评论的时刻归零。
//
// 长度限制只能在应用层做：SQLite 无法按「行类型」分别约束长度。
func (c *Comment) Validate() error {
	c.Content = strings.TrimSpace(c.Content)
	switch {
	case c.Path == "":
		return errors.New("comment path is empty")
	case c.Content == "":
		return errors.New("comment content is empty")
	case c.Type == CommentTypeComment:
		c.Offset = 0 // 评论不挂时刻
		return nil
	case c.Type == CommentTypeDanmaku:
		if n := utf8.RuneCountInString(c.Content); n > MaxDanmakuLength {
			return errors.Errorf("danmaku content is %d characters, limit %d", n, MaxDanmakuLength)
		}
		if c.Offset < 0 {
			return errors.New("danmaku offset must be >= 0")
		}
		return nil
	default:
		return errors.Errorf("unknown comment type %d", c.Type)
	}
}

// AddComment 新增一条评论或弹幕。
func AddComment(c *Comment) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	return errors.WithStack(GetSchema().Comment.Insert().One(c))
}

// ListComments 列出某媒体的评论（按时间正序）。
func ListComments(path string) ([]*Comment, error) {
	rows, err := GetSchema().Comment.
		Where("path = ? AND type = ?", path, CommentTypeComment).Select().All()
	if err != nil {
		return nil, errors.Wrap(err, "failed list comments")
	}
	sortByTime(rows)
	return rows, nil
}

// ListDanmaku 列出某媒体的弹幕（按出现时刻正序，播放器需要按时间轴喂）。
func ListDanmaku(path string) ([]*Comment, error) {
	rows, err := GetSchema().Comment.
		Where("path = ? AND type = ?", path, CommentTypeDanmaku).Select().All()
	if err != nil {
		return nil, errors.Wrap(err, "failed list danmaku")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Offset < rows[j].Offset })
	return rows, nil
}

// DeleteComment 删除自己的评论/弹幕；uid 用于防止越权删别人的。
func DeleteComment(id, uid uint) error {
	c, err := GetSchema().Comment.Where("id = ?", id).Select().One()
	if err != nil {
		if isNoRows(err) {
			return ErrRecordNotFound
		}
		return errors.Wrap(err, "failed find comment")
	}
	if c == nil {
		return ErrRecordNotFound
	}
	if c.UserID != uid {
		return errors.New("cannot delete other's comment")
	}
	return errors.WithStack(GetSchema().Comment.Delete().Where("id = ?", id).Exec())
}

func sortByTime(rows []*Comment) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
}
