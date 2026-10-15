package handlers

import (
	"path"
	"strconv"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/helpers"
	web "github.com/azhai/mocca/lib/web"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/pkg/errors"
)

// ListFavorites 我的收藏。
func ListFavorites(c *web.Context) error {
	all, err := models.ListFavorites(middlewares.UserID(c))
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取收藏失败")
	}
	return helpers.OK(c, all)
}

// AddFavorite 收藏。重复收藏不会产生第二条记录。
func AddFavorite(c *web.Context) error {
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
func RemoveFavorite(c *web.Context) error {
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
func danmakuTarget(c *web.Context, mediaPath string) (drivers.Driver, string, error) {
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
func authorSnapshot(c *web.Context) (uid uint, name, avatar string, err error) {
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
func AddDanmaku(c *web.Context) error {
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
func ListDanmaku(c *web.Context) error {
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
func AddComment(c *web.Context) error {
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
func ListComments(c *web.Context) error {
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
func DeleteComment(c *web.Context) error {
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

// ---------- 后台的弹幕/评论管理 ----------
//
// 浏览端只能发与删自己的（上面那几个接口）；后台要的是「找到哪部片子有东西 + 逐条删 +
// 按类型清空」。单条删除不另开接口：`DELETE /api/comments` 内部已经按 isAdmin 放行，
// 管理员拿自己的令牌就能删任何人的。另两个入口都在 admin 组里（纯 API 版整组不挂）。

// SocialStat 一个媒体的弹幕/评论条数。列目录探测（MediaObj.Social）与审核面板头部共用。
type SocialStat struct {
	Danmaku  int `json:"danmaku"`
	Comments int `json:"comments"`
}

// SocialDetail 后台审核一屏要的全部内容：一次读取给齐，拆两个请求就要读两遍文件。
type SocialDetail struct {
	SHA1     string                      `json:"sha1"`
	Counts   SocialStat                  `json:"counts"`
	Danmaku  []*mediaindex.Entry         `json:"danmaku"`
	Comments []*mediaindex.CommentThread `json:"comments"`
}

// socialCounts 探这一层每个媒体的弹幕/评论条数：以文件名为键，**只给非零的条目建值**
// —— 绝大多数目录一条弹幕都没有，返一个全 0 的 map 只会让响应里多出一堆空字段。
//
// 与 hls 探测（见 hlsCutNames）同一套做法：不落库、默认关、只有后台要。
// 区别是弹幕文件按 sha1 分片存在 `.mocca` 里，没有「一次 readdir 拿齐整层」的办法，
// 所以这里是一个媒体一次 MetaStat：没发过东西的片子就这一次往返。
func socialCounts(drv drivers.Driver, shas map[string]string) map[string]*SocialStat {
	out := map[string]*SocialStat{}
	for name, sha := range shas {
		if !mediaindex.IsSHA1(sha) {
			continue // 没索引就没指纹，也就没有弹幕文件可探
		}
		d, cm, err := mediaindex.DanmakuCounts(drv, sha)
		if err != nil || (d == 0 && cm == 0) {
			continue // 探不到就当没有：列目录不该因为一个读不了的元数据文件而失败
		}
		out[name] = &SocialStat{Danmaku: d, Comments: cm}
	}
	return out
}

// socialForDir 整目录列表用：自己开一次驱动去探。虚拟层（不在任何挂载点里）没得可探。
func socialForDir(dir string, shas map[string]string) map[string]*SocialStat {
	st, _, err := resolveStorageStrict(dir)
	if err != nil {
		return nil
	}
	drv, err := drivers.Open(st)
	if err != nil {
		return nil
	}
	defer func() { _ = drv.Close() }()
	return socialCounts(drv, shas)
}

// FsSocial 后台看某个媒体的弹幕与评论（全部，不按时间轴窗口筛）。
func FsSocial(c *web.Context) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	drv, sha, err := danmakuTarget(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	defer func() { _ = drv.Close() }()

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取弹幕失败")
	}
	d := &SocialDetail{
		SHA1: sha,
		// 两个列表都初始化为空数组而不是 nil：前端直接 .map()，不想先判存在
		Danmaku:  []*mediaindex.Entry{},
		Comments: mediaindex.CommentThreads(rows),
	}
	for _, e := range rows {
		if e.IsDanmaku() {
			d.Counts.Danmaku++
			d.Danmaku = append(d.Danmaku, e)
			continue
		}
		d.Counts.Comments++ // 含回复：被删掉顶层评论的孤儿回复不展示，但确实占一条记录
	}
	return helpers.OK(c, d)
}

// FsSocialClear 清空某个媒体的弹幕或评论。kind 只认 danmaku / comment / all。
//
// kind **不给默认值**：漏字段就清两类是数据丢失类的坑，宁可报错让人再点一次。
// 清空是整文件重写，没有回收站；前端二次确认，这里不再多一道询问。
func FsSocialClear(c *web.Context) error {
	var req struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	only := mediaindex.DanmakuAll
	switch req.Kind {
	case "danmaku":
		only = mediaindex.DanmakuTypeDanmaku
	case "comment":
		only = mediaindex.DanmakuTypeComment
	case "all":
	default:
		return helpers.Fail(c, helpers.CodeBadRequest, "kind 只认 danmaku / comment / all")
	}
	drv, sha, err := danmakuTarget(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	defer func() { _ = drv.Close() }()

	n, err := mediaindex.ClearDanmaku(drv, sha, only)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "清空失败")
	}
	return helpers.OK(c, map[string]int{"cleared": n})
}
