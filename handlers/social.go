package handlers

import (
	"path"
	"strconv"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// ListFavorites 我的收藏。
func ListFavorites(c *echo.Context) error {
	all, err := models.ListFavorites(middlewares.UserID(c))
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取收藏失败")
	}
	return helpers.OK(c, all)
}

// AddFavorite 收藏。重复收藏不会产生第二条记录。
func AddFavorite(c *echo.Context) error {
	var req struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Kind  int    `json:"kind"`
		Thumb string `json:"thumb"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	f := &models.Favorite{
		UserID: middlewares.UserID(c),
		Path:   req.Path,
		Name:   req.Name,
		Kind:   req.Kind,
		Thumb:  req.Thumb,
	}
	if err := models.AddFavorite(f); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "收藏失败")
	}
	return helpers.OK(c, nil)
}

// RemoveFavorite 取消收藏。
func RemoveFavorite(c *echo.Context) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	if err := models.RemoveFavorite(middlewares.UserID(c), req.Path); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "取消收藏失败")
	}
	return helpers.OK(c, nil)
}

// ---------- 弹幕与评论 ----------
//
// 数据**不在数据库里**：落在设备侧 `<存储根>/.mocca/xx/xx/<sha1>.danmaku.jsonl`
// （见 mediaindex/danmaku.go）。这样弹幕跟着盘走 —— 换机器、重装服务、把硬盘拔下来
// 带走都还在；按 sha1 锚定，文件改名或移动也不会丢。
// 评论就是「时间点为 0 的弹幕」，两者同一份存储、同一套结构。

// ErrNeedIndex 该媒体还没被索引，拿不到内容指纹。
var ErrNeedIndex = errors.New("该媒体还没有索引（.index.jsonl），请先索引；弹幕按内容指纹锚定，之后改名、移动都不会丢")

// sha1OfMedia 从父目录的 .index.jsonl 取该文件的 sha1；取不到返回空串。
// 与 mediaindex 的海报/简介同一套寻址方式。
func sha1OfMedia(drv drivers.Driver, rel string) string {
	parent, base := path.Dir(rel), path.Base(rel)
	idx := mediaindex.IndexFileName
	if parent != "" && parent != "." && parent != "/" {
		idx = parent + "/" + idx
	}
	f, _, err := drv.Open(idx)
	if err != nil {
		return ""
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	recs, err := mediaindex.ReadIndex(f)
	if err != nil {
		return ""
	}
	if rec, ok := recs[base]; ok && mediaindex.IsSHA1(rec.SHA1) {
		return rec.SHA1
	}
	return ""
}

// danmakuTarget 把「某个媒体的弹幕」解析成 (驱动, sha1)。
// 出错返回普通 error，由调用方决定映射成哪个响应码。
func danmakuTarget(c *echo.Context, mediaPath string) (drivers.Driver, string, error) {
	scoped, err := scopePath(c, mediaPath)
	if err != nil {
		return nil, "", err
	}
	st, rel, err := resolveStorageStrict(scoped)
	if err != nil {
		return nil, "", errors.New("找不到该媒体所在的存储")
	}
	drv, err := drivers.Open(st)
	if err != nil {
		return nil, "", errors.Wrap(err, "打开存储失败")
	}
	sha := sha1OfMedia(drv, rel)
	if sha == "" {
		_ = drv.Close()
		return nil, "", ErrNeedIndex
	}
	return drv, sha, nil
}

// authorSnapshot 取**当前**用户的昵称与头像快照。
//
// 为什么要快照进每条记录：弹幕存在文件里，列表时没有用户表可 join；
// 而且快照正是要的效果 —— 用户以后改昵称、换头像，历史弹幕仍显示当时的样子。
func authorSnapshot(c *echo.Context) (uid uint, name, avatar string, err error) {
	uid = middlewares.UserID(c)
	if uid == 0 {
		return 0, "", "", errors.New("未登录")
	}
	u, err := models.GetUserByID(uid)
	if err != nil || u == nil {
		return uid, "", "", errors.New("找不到该用户")
	}
	return uid, u.Username, u.AvatarOrDefault(), nil
}

// isAdmin 当前用户是否管理员（角色不放令牌里，要查库，见 middlewares.AdminMiddleware）。
func isAdmin(uid uint) bool {
	u, err := models.GetUserByID(uid)
	return err == nil && u != nil && u.IsAdmin()
}

// AddDanmaku 发一条弹幕：content + 时间点 offset（毫秒）。
func AddDanmaku(c *echo.Context) error {
	uid, name, avatar, err := authorSnapshot(c)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}
	var req struct {
		Path    string `json:"path"`
		Offset  int    `json:"offset"` // 毫秒：弹幕在时间轴上出现的时刻
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	drv, sha, err := danmakuTarget(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	defer func() { _ = drv.Close() }()

	e := &mediaindex.Entry{
		UserID: uid, UserName: name, UserAvatar: avatar,
		Type: mediaindex.DanmakuTypeDanmaku, Offset: req.Offset, Content: req.Content,
	}
	if err := mediaindex.AppendDanmaku(drv, sha, e); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	mediaindex.PublishDanmaku(drv.Identity(), sha, e) // 推给正在看这个视频的人
	return helpers.OK(c, e)
}

// ListDanmaku 取某媒体的弹幕，可按时间轴取窗口：
// `?from=&to=` 毫秒区间（含端点）；不给就是全部。播放器按当前播放位置滚动取用。
func ListDanmaku(c *echo.Context) error {
	mediaPath := c.QueryParam("path")
	if mediaPath == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	drv, sha, err := danmakuTarget(c, mediaPath)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	defer func() { _ = drv.Close() }()

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取弹幕失败")
	}
	from, _ := strconv.Atoi(c.QueryParam("from"))
	to := -1 // 负数 = 不设上界
	if s := c.QueryParam("to"); s != "" {
		to, _ = strconv.Atoi(s)
	}
	return helpers.OK(c, mediaindex.DanmakuInRange(rows, from, to))
}

// AddComment 发评论；带 parent_id 就是回复某条评论（回复只支持一层）。
func AddComment(c *echo.Context) error {
	uid, name, avatar, err := authorSnapshot(c)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}
	var req struct {
		Path     string `json:"path"`
		Content  string `json:"content"`
		ParentID string `json:"parent_id"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	drv, sha, err := danmakuTarget(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	defer func() { _ = drv.Close() }()

	e := &mediaindex.Entry{
		UserID: uid, UserName: name, UserAvatar: avatar,
		Type: mediaindex.DanmakuTypeComment, Content: req.Content, ParentID: req.ParentID,
	}
	if err := mediaindex.AppendDanmaku(drv, sha, e); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	mediaindex.PublishDanmaku(drv.Identity(), sha, e)
	return helpers.OK(c, e)
}

// ListComments 某媒体的评论：顶层评论按时间正序，每条带它的回复。
func ListComments(c *echo.Context) error {
	mediaPath := c.QueryParam("path")
	if mediaPath == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	drv, sha, err := danmakuTarget(c, mediaPath)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	defer func() { _ = drv.Close() }()

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取评论失败")
	}
	return helpers.OK(c, mediaindex.CommentThreads(rows))
}

// DeleteComment 删除自己发的评论/弹幕；管理员可以删任何人的。
func DeleteComment(c *echo.Context) error {
	uid := middlewares.UserID(c)
	if uid == 0 {
		return helpers.Fail(c, helpers.CodeUnauthorized, "未登录")
	}
	var req struct {
		Path string `json:"path"`
		ID   string `json:"id"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" || req.ID == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	drv, sha, err := danmakuTarget(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	defer func() { _ = drv.Close() }()

	if err := mediaindex.DeleteDanmaku(drv, sha, req.ID, uid, isAdmin(uid)); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	return helpers.OK(c, nil)
}
