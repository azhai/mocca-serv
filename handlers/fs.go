package handlers

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// ListReq 列目录请求。
type ListReq struct {
	Path     string `json:"path"`
	Password string `json:"password"`
	Page     int    `json:"page"`
	PerPage  int    `json:"per_page"`
}

// GetReq 取详情请求。
type GetReq struct {
	Path     string `json:"path"`
	Password string `json:"password"`
}

// MediaObj 目录条目。
type MediaObj struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	Thumb    string `json:"thumb"`
	Type     int    `json:"type"`
	// Mount 标记这一条是**挂载点**（由挂载点聚合树虚拟出来的目录），
	// 不是存储里的真实目录。客户端据此换一个图标，别让用户以为两者是一回事。
	Mount bool `json:"mount,omitempty"`
	// Path 挂载点的真实导航路径。展示名（Name）可能与它脱钩：当挂载点
	// 与同层某个真实子目录重名时，展示名会加「～」前缀以示区分，但点进去
	// 仍应落到真实路径，所以单独再给一条 Path。
	Path string `json:"path,omitempty"`
	// Sign 受保护路径的签名。当前用「目录密码 + 令牌」保护，
	// 故恒为空；保留字段是为了让 APP 的 `sign` 解析路径始终有值可选。
	Sign string `json:"sign"`
	// SHA1 媒体文件内容指纹，来自所在目录的 .index.jsonl（见 mediaindex）。
	// 没有索引文件或该文件未入列则为空；前端据此拼 .mocca 海报图地址。
	SHA1 string `json:"sha1,omitempty"`
}

// MediaDetail 对象详情：在列表条目基础上追加播放地址。
//
// Header 是 APP `parsedHeaders()` 要的多行 `Key: Value` 字符串，**不是 map**：
// 写成 map 会让 APP 侧解析失败。Provider 同理，缺失时 APP 取默认值。
type MediaDetail struct {
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	IsDir    bool     `json:"is_dir"`
	Type     int      `json:"type"`
	Thumb    string   `json:"thumb"`
	Sign     string   `json:"sign"`
	Modified string   `json:"modified"`
	RawURL   string   `json:"raw_url"`
	Header   string   `json:"header"`
	Provider string   `json:"provider"`
	Title    string   `json:"title,omitempty"`
	Duration int      `json:"duration,omitempty"`
	Cover    string   `json:"cover,omitempty"`
	Desc     string   `json:"description,omitempty"`
	Authors  []string `json:"authors,omitempty"`
}

// resolveStorage 按挂载点最长匹配选中存储，并算出存储内的相对路径。
// 额外返回存储本身，供「跨存储移动」这类需要比对来源与目标的场景使用。
func resolveStorage(reqPath string) (*models.Storage, string, error) {
	reqPath = ensureLeadingSlash(reqPath)

	storages, err := models.ListStorages()
	if err != nil {
		return nil, "", errors.WithStack(err)
	}
	var matched, fallback *models.Storage
	for _, s := range storages {
		if s.Disabled {
			continue
		}
		if fallback == nil || len(s.MountPath) > len(fallback.MountPath) {
			fallback = s
		}
		if reqPath == s.MountPath || strings.HasPrefix(reqPath, s.MountPath+"/") {
			if matched == nil || len(s.MountPath) > len(matched.MountPath) {
				matched = s
			}
		}
	}
	target := matched
	if target == nil {
		target = fallback
	}
	if target == nil {
		return nil, "", errors.New("尚未配置存储，请先添加挂载点")
	}

	rel := strings.TrimPrefix(reqPath, target.MountPath)
	if rel == "" {
		rel = "/"
	}
	return target, rel, nil
}

// openStorage 同上并打开驱动。返回的 Driver 由调用方负责 Close
// （SMB 的 Close 只是归还连接池里的引用，不会断开连接）。
func openStorage(reqPath string) (drivers.Driver, string, error) {
	st, rel, err := resolveStorage(reqPath)
	if err != nil {
		return nil, "", err
	}
	drv, err := drivers.Open(st)
	if err != nil {
		return nil, "", err
	}
	return drv, rel, nil
}

// resolveStorageStrict 只认「路径确实落在某个挂载点之内」的情况：最长前缀匹配，
// 不做任何兜底。用来区分两种路径 ——
//   - 属于某个存储的真实路径（例如 /media/movies）；
//   - 只是聚合树上的中间节点（例如只挂了 /media 时的根路径 /）。
func resolveStorageStrict(reqPath string) (*models.Storage, string, error) {
	reqPath = ensureLeadingSlash(reqPath)

	storages, err := models.ListStorages()
	if err != nil {
		return nil, "", errors.WithStack(err)
	}
	var best *models.Storage
	bestMount := ""
	for _, s := range storages {
		if s.Disabled {
			continue
		}
		// 逐个按规范化后的挂载点比较：即使库里存着历史的不规范写法也不会漏配
		mount := models.NormalizeMountPath(s.MountPath)
		// 根挂载点天然是任何路径的前缀，但只在没有更长的匹配时才用它
		if mount != "/" && reqPath != mount && !strings.HasPrefix(reqPath, mount+"/") {
			continue
		}
		if best == nil || len(mount) > len(bestMount) {
			best, bestMount = s, mount
		}
	}
	if best == nil {
		return nil, "", errors.New("路径不属于任何挂载点")
	}

	rel := strings.TrimPrefix(reqPath, bestMount)
	if rel == "" {
		rel = "/"
	}
	return best, rel, nil
}

// objType 判定条目类型。
//
// 目录必须显式返回 0，不能靠扩展名猜：一个叫 movies.mp4 的目录
// 若按扩展名判成视频，APP 就会把它当成可播放文件。
func objType(name string, isDir bool) int {
	if isDir {
		return models.MediaDir
	}
	return guessType(name)
}

// systemReservedNames 系统保留的文件/目录名，按**完整名字**匹配（不区分大小写），
// 命中即不下发。键统一小写。
//
// 只收「确定由系统生成、且不会与正常媒体重名」的那些名字。刻意不用「首字符是
// 特殊符号」这类宽规则：`!`/`#`/`%` 开头的正常文件并不少见（`#1 Hits.mp4`、
// `!important.mp3`），按首字符挡会连它们一起误伤。
var systemReservedNames = []string{
	"$recycle.bin",              // Windows 回收站（就长在盘根目录，最常见）
	"system volume information", // Windows 卷信息
	"recycler",                  // 旧版 Windows 回收站
	"thumbs.db",                 // Windows 缩略图缓存
	"ehthumbs.db",
	"desktop.ini",  // Windows 目录配置
	"lost+found",   // ext 文件系统
	"found.000",    // chkdsk 恢复出来的碎片
	"hiberfil.sys", // 休眠/分页文件
	"pagefile.sys",
	"swapfile.sys",
	"network trash folder", // macOS 在网络卷上生成
	"temporary items",
}

// isExcludedName 判断条目是否不下发，两条规则任一命中即排除：
//
//  1. 以点（.）开头 —— 隐藏文件与隐藏目录，含 .DS_Store、._xxx（macOS 资源
//     分支）、.git，以及本服务自己建的 .mocca（封面/缩略图，见
//     models.HiddenDirName，它就在数据目录下，不挡会出现在浏览列表里）；
//  2. 完整名字命中 systemReservedNames（不区分大小写），如盘根的 $RECYCLE.BIN。
//
// 除此之外不按字符做任何判断：名字里凡是不构成上述两条的符号一律保留。
//
// 只按名字判断，不看隐藏属性位：Local 与 SMB 对隐藏属性的语义并不一致。
// 另外过滤只作用于「列表」，按显式路径取流（/d/.mocca/covers/x.jpg）
// 仍然照常可用，否则封面图会跟着挂掉。
func isExcludedName(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return slices.Contains(systemReservedNames, strings.ToLower(name))
}

// providerName 把存储驱动名转成 APP 展示用的 provider 值。
func providerName(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "smb", "samba":
		return "SMB"
	case "", "local":
		return "Local"
	default:
		return driver
	}
}

// guessType 按扩展名猜媒体类型，未知返回 MediaUnknown(1) —— 不是 0，
// 0 在契约里专指目录。
func guessType(name string) int {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".flv", ".m3u8", ".ts":
		return models.MediaVideo
	case ".mp3", ".flac", ".wav", ".aac", ".ogg", ".m4a", ".wma":
		return models.MediaAudio
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".avif":
		return models.MediaImage
	case ".txt", ".md", ".srt", ".ass", ".vtt", ".json", ".nfo":
		return models.MediaText
	}
	return models.MediaUnknown
}

// FsList 列目录。
//
// 返回的 content 由两部分拼成：
//  1. 挂载点聚合树给出的虚拟目录（models 内存树，只含挂载点层级，**永远排最前**）；
//  2. 真实条目 —— 只有路径确实落在某个挂载点之内时，才去读那个存储。
func FsList(c *echo.Context) error {
	var req ListReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	if req.Path == "" {
		req.Path = "/"
	}
	// 普通用户被收敛到自己目录；未登录按 allow_guest 开关决定放行
	scoped, err := scopePath(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}

	// 目录密码先于存储访问校验，避免未授权就触碰后端
	if err := requireFolderPassword(scoped, req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeForbidden, err.Error())
	}

	objs, err := listDir(models.NormalizeMountPath(scoped))
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	// total 取过滤后的条数，与 content 保持一致。
	// is_mount 告诉客户端「这个目录自己是不是一个挂载点」——根没被挂存储时
	// 它只是聚合树的虚拟根，根被挂了存储（mount_path=/）时它是挂载点。
	return helpers.OK(c, map[string]any{
		"content": objs, "total": len(objs),
		"is_mount": models.IsMount(scoped),
	})
}

// listDir 组装一层目录的条目：挂载点在最前，其后是排序过的真实条目。
func listDir(dir string) ([]MediaObj, error) {
	// 先读真实条目，才能知道这一层哪些名字会与挂载点重名
	entries, err := realEntries(dir, len(models.MountChildren(dir)) > 0)
	if err != nil {
		return nil, err
	}
	realNames := make(map[string]bool, len(entries))
	for _, e := range entries {
		realNames[e.Name] = true
	}

	// 挂载点条目只来自内存里的聚合树：带 mount 标记，客户端才能与真实目录区分开。
	// 挂载点与同层真实子目录允许重名（只要求挂载点路径各自不重复）：
	// 重名的挂载点展示名前面加「～」区分，真实那条照常保留，两者都点得进。
	mounts := models.MountChildren(dir)
	mountObjs := make([]MediaObj, 0, len(mounts))
	for _, name := range mounts {
		path := dir + "/" + name
		if dir == "/" {
			path = "/" + name
		}
		display := name
		if realNames[name] {
			display = "～" + name
		}
		mountObjs = append(mountObjs, MediaObj{
			Name: display, Path: path, IsDir: true, Type: models.MediaDir, Mount: true,
		})
	}

	rest := make([]MediaObj, 0, len(entries))
	shas := dirSHA1s(dir)
	for _, e := range entries {
		rest = append(rest, MediaObj{
			Name:     e.Name,
			Size:     e.Size,
			IsDir:    e.IsDir,
			Modified: e.Modified.Format(time.RFC3339),
			Type:     objType(e.Name, e.IsDir),
			SHA1:     shas[e.Name],
		})
	}
	sortObjs(rest)

	// 挂载点已按名字排好（聚合树保证），真实条目接在后面
	return append(mountObjs, rest...), nil
}

// realEntries 读这一层的真实条目（点开头与系统保留名在这一层就剔除）。
//
//	路径落在某个挂载点之内         → 读那个挂载点，相对路径 = 去掉挂载点前缀
//	不在任何挂载点内、但这一层有挂载点 → 不读存储：这一层是纯虚拟的（例如根层）
//	两者都不满足                   → 退回最长挂载点兜底，保持历史路径可用
func realEntries(dir string, hasMounts bool) ([]drivers.Entry, error) {
	st, rel, err := resolveStorageStrict(dir)
	if err != nil {
		if hasMounts {
			return nil, nil
		}
		if st, rel, err = resolveStorage(dir); err != nil {
			return nil, err
		}
	}
	drv, err := drivers.Open(st)
	if err != nil {
		return nil, err
	}
	defer func() { _ = drv.Close() }()

	entries, err := drv.List(rel)
	if err != nil {
		return nil, errors.New("目录不存在")
	}
	out := make([]drivers.Entry, 0, len(entries))
	for _, e := range entries {
		if isExcludedName(e.Name) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// dirSHA1s 读某目录的 .index.jsonl，返回「文件名 → sha1」。读不到（无索引、无该文件、
// 非法 sha1）一律留空，不阻断列目录 —— 索引是可选增强，缺了只是没海报。
func dirSHA1s(dir string) map[string]string {
	st, rel, err := resolveStorageStrict(dir)
	if err != nil {
		return nil
	}
	drv, err := drivers.Open(st)
	if err != nil {
		return nil
	}
	defer func() { _ = drv.Close() }()

	idxRel := strings.TrimSuffix(rel, "/") + "/" + mediaindex.IndexFileName
	f, _, err := drv.Open(idxRel)
	if err != nil {
		return nil
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	recs, err := mediaindex.ReadIndex(f)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(recs))
	for name, rec := range recs {
		if mediaindex.IsSHA1(rec.SHA1) {
			out[name] = rec.SHA1
		}
	}
	return out
}

// sortObjs 真实条目的展示顺序：目录在前，其后按文件类型分组
// （视频 → 音频 → 图片 → 文本 → 其它），每组内按名字的字母序。
func sortObjs(objs []MediaObj) {
	sort.SliceStable(objs, func(i, j int) bool {
		a, b := objs[i], objs[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		if ra, rb := fileRank(a.Type), fileRank(b.Type); ra != rb {
			return ra < rb
		}
		return models.LessName(a.Name, b.Name)
	})
}

// fileRank 文件的分组顺序。目录（type=0）走另一条分支，不会到这里。
func fileRank(t int) int {
	switch t {
	case models.MediaVideo:
		return 0
	case models.MediaAudio:
		return 1
	case models.MediaImage:
		return 2
	case models.MediaText:
		return 3
	default: // unknown(1) 等一律归到「其它」
		return 4
	}
}

// FsGet 取对象详情，raw_url 可直接交给播放器。
func FsGet(c *echo.Context) error {
	var req GetReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	scoped, err := scopePath(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}
	if err := requireFolderPassword(scoped, req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeForbidden, err.Error())
	}

	// 这里改用 resolveStorage：既要打开驱动，也要拿到存储本身来填 provider
	stor, rel, err := resolveStorage(scoped)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	info, err := drv.Stat(rel)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "文件不存在")
	}
	detail := MediaDetail{
		Name:     info.Name,
		Size:     info.Size,
		IsDir:    info.IsDir,
		Type:     objType(info.Name, info.IsDir),
		Modified: info.Modified.Format(time.RFC3339),
		Provider: providerName(stor.Driver),
		RawURL:   rawURL(c, req.Path),
	}

	// 元数据是可选增强：没有就只回基础字段，不让前端拿到一堆零值
	if meta, err := models.GetMediaByPath(req.Path); err == nil && meta != nil {
		detail.Title = meta.Title
		detail.Duration = meta.Duration
		detail.Cover = meta.Cover
		detail.Desc = meta.Description
		if authors, err := models.ListAuthors(meta.ID); err == nil {
			detail.Authors = authors
		}
	}
	return helpers.OK(c, detail)
}

// mediaExtraInfo 读某文件的 .mocca 附加信息：从父目录 .index.jsonl 取 sha1，
// 再据此判断海报是否存在、并读回简介正文。三项任一缺失一律给零值，绝不阻断调用方。
func mediaExtraInfo(drv drivers.Driver, fileRel string) (sha1, summary string, posterOK bool) {
	parent, base := path.Dir(fileRel), path.Base(fileRel)
	idx := mediaindex.IndexFileName
	if parent != "" && parent != "." && parent != "/" {
		idx = parent + "/" + idx
	}
	if f, _, err := drv.Open(idx); err == nil {
		recs, rerr := mediaindex.ReadIndex(f)
		_ = f.(interface{ Close() error }).Close()
		if rerr == nil {
			if rec, ok := recs[base]; ok && mediaindex.IsSHA1(rec.SHA1) {
				sha1 = rec.SHA1
			}
		}
	}
	if sha1 == "" {
		return "", "", false
	}
	if posterRel, err := mediaindex.PosterRel(sha1); err == nil {
		if _, serr := drv.Stat(posterRel); serr == nil {
			posterOK = true
		}
	}
	if sumRel, err := mediaindex.SummaryRel(sha1); err == nil {
		if sf, _, serr := drv.Open(sumRel); serr == nil {
			b, _ := io.ReadAll(sf)
			_ = sf.(interface{ Close() error }).Close()
			summary = string(b)
		}
	}
	return sha1, summary, posterOK
}

// FsInfo 单文件详情增强（悬浮层一次请求拿全）：聚合 .index.jsonl 的 sha1、
// .mocca 海报/简介，以及数据库里的元数据与全体人员。
func FsInfo(c *echo.Context) error {
	var req GetReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	scoped, err := scopePath(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}
	if err := requireFolderPassword(scoped, req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeForbidden, err.Error())
	}

	stor, rel, err := resolveStorageStrict(scoped)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	info, err := drv.Stat(rel)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "文件不存在")
	}
	sha1, summary, posterOK := mediaExtraInfo(drv, rel)

	resp := map[string]any{
		"name": info.Name, "is_dir": info.IsDir, "type": objType(info.Name, info.IsDir),
		"modified": info.Modified.Format(time.RFC3339), "size": info.Size,
		"sha1": sha1, "poster": posterOK, "summary": summary, "meta": nil,
	}
	if meta, err := models.GetMediaByPath(scoped); err == nil && meta != nil {
		people, _ := models.ListPeople(meta.ID)
		resp["meta"] = map[string]any{
			"title": meta.Title, "description": meta.Description,
			"director": meta.Director, "year": meta.Year,
			"region": meta.Region, "studio": meta.Studio, "language": meta.Language,
			// 按角色分组回给前端：cast=主演(视频)、lead/backing/instrument=主唱/伴唱/演奏(音频)
			"cast":       namesByRole(people, ""),
			"lead":       namesByRole(people, models.RoleLead),
			"backing":    namesByRole(people, models.RoleBacking),
			"instrument": namesByRole(people, models.RoleInstrument),
		}
	}
	return helpers.OK(c, resp)
}

// namesByRole 从人员里抽某角色的名字序列（空串角色即主演）。
func namesByRole(people []models.AuthorPerson, role string) []string {
	out := []string{}
	for _, p := range people {
		if p.Role == role {
			out = append(out, p.Name)
		}
	}
	return out
}

// MetaPoster 输出某文件的 .mocca 海报图（PNG）。无海报返回 404。
// 用 StreamAuth 装配（routes）：令牌可放 ?token=，供 <img> 直接加载。
func MetaPoster(c *echo.Context) error {
	reqPath := c.QueryParam("path")
	if reqPath == "" {
		return helpers.FailStatus(c, helpers.CodeBadRequest, "缺少 path")
	}
	// <img> 带不了自定义头，目录密码也只能从查询串取；StreamAuth 已解析过令牌，
	// 这里再单独做路径收敛与目录密码校验（与 Download 一致）。
	scoped, err := scopePath(c, reqPath)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeUnauthorized, err.Error())
	}
	if err := requireFolderPassword(scoped, c.QueryParam("password")); err != nil {
		return helpers.FailStatus(c, helpers.CodeForbidden, err.Error())
	}
	stor, rel, err := resolveStorageStrict(scoped)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	sha1, _, _ := mediaExtraInfo(drv, rel)
	if sha1 == "" {
		return helpers.FailStatus(c, helpers.CodeNotFound, "无海报")
	}
	posterRel, err := mediaindex.PosterRel(sha1)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeNotFound, "无海报")
	}
	f, _, err := drv.Open(posterRel)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeNotFound, "无海报")
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	c.Response().Header().Set("Content-Type", "image/png")
	http.ServeContent(c.Response(), c.Request(), "poster.png", time.Now(), f)
	return nil
}

// FsEdit 管理员的「编辑」：可选改名（真改磁盘，只动不含扩展名的部分），
// 并整体写入附加信息（简介/导演/主演/年份/地区/出品方）。改名时同步迁移元数据。
func FsEdit(c *echo.Context) error {
	var req struct {
		Path        string   `json:"path"`
		Name        string   `json:"name"` // 不含扩展名的新文件名；空或未变则不移动
		Description string   `json:"description"`
		Director    string   `json:"director"`
		Cast        []string `json:"cast"` // 主演（视频）
		Year        int      `json:"year"`
		Region      string   `json:"region"`
		Studio      string   `json:"studio"`
		Language    string   `json:"language"`   // 语言（音频）
		Lead        []string `json:"lead"`       // 主唱（音频）
		Backing     []string `json:"backing"`    // 伴唱（音频）
		Instrument  []string `json:"instrument"` // 演奏（音频）
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}

	scoped, err := scopePath(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}
	stor, rel, err := resolveStorageStrict(scoped)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	newPath := scoped
	oldBase := path.Base(rel)
	// 改名：只改不含扩展名的部分，扩展名原样保留。
	if base := stripExt(oldBase); req.Name != "" && req.Name != base {
		target := req.Name + strings.ToLower(path.Ext(oldBase))
		if err := drv.Rename(rel, target); err != nil {
			return helpers.Fail(c, helpers.CodeInternal, "改名失败: "+err.Error())
		}
		// 迁移受保护/普通路径：算目录级路由，再拼新文件名
		dirPath := path.Dir(scoped)
		if dirPath == "." {
			dirPath = "/"
		}
		newPath = path.Join(dirPath, target)
		rel = path.Join(path.Dir(rel), target)
	}

	info, err := drv.Stat(rel)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "文件不存在")
	}
	// 文字/未知类可改名，但不建媒体元数据（IsValidMediaKind 外的类型不入库）
	if kind := objType(info.Name, false); !models.IsValidMediaKind(kind) {
		return helpers.OK(c, map[string]any{"path": newPath, "name": info.Name})
	}

	meta, err := models.GetMediaByPath(scoped)
	if err != nil {
		meta = &models.MediaMeta{
			Path: newPath, Kind: objType(info.Name, false),
			Size: info.Size, Title: req.Name,
		}
	}
	// 改名后旧行迁移到新路径，避免留下孤儿元数据
	if newPath != scoped && meta.Path == scoped {
		meta.Path = newPath
	}
	meta.Title = req.Name
	meta.Duration = 0 // 时长未知，编辑不填就置空
	meta.Description = req.Description
	meta.Director = req.Director
	meta.Year = req.Year
	meta.Region = req.Region
	meta.Studio = req.Studio
	meta.Language = req.Language

	// 角色分组落库：视频 cast=主演(空角色)、音频 lead/backing/instrument 各占一个角色
	people := []models.AuthorPerson{}
	addPeople := func(names []string, role string) {
		for _, n := range names {
			people = append(people, models.AuthorPerson{Name: n, Role: role})
		}
	}
	addPeople(req.Cast, "")
	addPeople(req.Lead, models.RoleLead)
	addPeople(req.Backing, models.RoleBacking)
	addPeople(req.Instrument, models.RoleInstrument)
	if err := applyPeople(meta, people); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "保存元数据失败: "+err.Error())
	}
	return helpers.OK(c, map[string]any{"path": newPath, "name": info.Name})
}

// applyPeople 落库一条媒体元数据及其人员（有则更、无则建，再整体替换人员）。
func applyPeople(meta *models.MediaMeta, people []models.AuthorPerson) error {
	if meta.ID == 0 {
		if err := models.CreateMedia(meta, nil); err != nil {
			return err
		}
	} else if err := models.UpdateMedia(meta); err != nil {
		return err
	}
	return models.SetPeople(meta.ID, people)
}

// stripExt 去文件名扩展名（.mp4 之类）。无扩展名原样返回。
func stripExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// rawURL 拼播放地址：/d/<路径>?token=<当前令牌>。
// 令牌放查询串是为了照顾不能自定义请求头的播放器。
func rawURL(c *echo.Context, path string) string {
	q := make(url.Values)
	if tok := middlewares.TokenFrom(c.Request().Header.Get("Authorization")); tok != "" {
		q.Set("token", tok)
	}
	u := "/d" + ensureLeadingSlash(path)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func ensureLeadingSlash(p string) string {
	if p == "" || strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// FsRemove 删除文件或目录（管理员）。目录会递归删除，调用方需自行确认。
func FsRemove(c *echo.Context) error {
	var req GetReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	drv, rel, err := openStorage(req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	defer func() { _ = drv.Close() }()

	if err := drv.Remove(rel); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "删除失败: "+err.Error())
	}
	// 同步清掉数据库里的元数据与人员，避免留下指向已删文件的脏行
	if meta, err := models.GetMediaByPath(req.Path); err == nil && meta != nil {
		_ = models.DeleteMedia(meta.ID)
	}
	return helpers.OK(c, nil)
}

// FsCov 上传替换音/视频封面（管理员）。写入该文件 sha1 对应的 .mocca 海报位置，
// 覆盖旧图；网格与悬浮层读 /meta/poster 时会直接看到新图。需先执行索引生成 .index.jsonl。
func FsCov(c *echo.Context) error {
	reqPath := c.QueryParam("path")
	if reqPath == "" {
		reqPath = c.FormValue("path")
	}
	if reqPath == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	stor, rel, err := resolveStorageStrict(reqPath)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	sha1, _, _ := mediaExtraInfo(drv, rel)
	if sha1 == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请先执行索引生成 .index.jsonl")
	}
	posterRel, err := mediaindex.PosterRel(sha1)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "无法定位封面路径")
	}

	// 兼容 multipart(file) 与裸 body 两种上传方式
	var data []byte
	if file, _, ferr := c.Request().FormFile("file"); ferr == nil {
		defer func() { _ = file.Close() }()
		data, err = io.ReadAll(file)
	} else {
		data, err = io.ReadAll(c.Request().Body)
	}
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "读取上传失败")
	}
	if len(data) == 0 || len(data) > 5<<20 {
		return helpers.Fail(c, helpers.CodeBadRequest, "封面大小须在 5MB 以内")
	}
	// 校验确实是可解码的图片，避免写入垃圾字节撑大体积
	img, _, derr := image.Decode(bytes.NewReader(data))
	_ = img
	if derr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "不是有效的图片文件")
	}

	w, werr := drv.Create(posterRel)
	if werr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "写入失败: "+werr.Error())
	}
	defer func() { _ = w.Close() }()
	if _, werr = w.Write(data); werr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "写入失败: "+werr.Error())
	}
	return helpers.OK(c, nil)
}

// writePosterBytes 把已校验的图片字节写入某文件的 .mocca 海报位置，幂等覆盖旧图。
// 校验由各调用方各自负责（上传前的图片解码 / 截图后的输出校验）。
func writePosterBytes(drv drivers.Driver, posterRel string, data []byte) error {
	w, err := drv.Create(posterRel)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	_, err = w.Write(data)
	return err
}

// localRealPath 若媒体在本地磁盘，返回其真实绝对路径，供 ffmpeg 直接读以实现快速定位
// （省去整份流式拷贝 + 从头解码丢弃）。非本地驱动返回空串。
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

// ffmpegShot 调 ffmpeg 从视频流里抽一帧 PNG 文本。
// -ss 放在 -i 前：本地真实路径可快速定位；管道输入则解码到目标时间点出帧。
// 改 -i 实参能兼顾「本地路径直读」与「SMB 等远程走 stdin 流」两种介质。
func ffmpegShot(src io.Reader, inputArg, sec string) ([]byte, error) {
	var stdin io.Reader
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", sec}
	if inputArg == "" {
		inputArg = "pipe:0"
		stdin = src
	}
	args = append(args, "-i", inputArg, "-frames:v", "1",
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

// FsShot 用 FFmpeg 按指定秒数从视频抽一帧，写入 .mocca 封面位置（管理员）。
// 与 FsCov 一样依赖 .index.jsonl 定位 sha1；入参 path + sec（秒，可小数）。
func FsShot(c *echo.Context) error {
	reqPath := c.QueryParam("path")
	if reqPath == "" {
		reqPath = c.FormValue("path")
	}
	if reqPath == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	sec := c.QueryParam("sec")
	if sec == "" {
		sec = c.FormValue("sec")
	}
	if sec == "" {
		sec = "0"
	}
	// 秒数必须是有效数字，负数一律按 0 处理
	if f, perr := strconv.ParseFloat(sec, 64); perr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "截图秒数无效")
	} else if f < 0 {
		sec = "0"
	}

	stor, rel, err := resolveStorageStrict(reqPath)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	info, err := drv.Stat(rel)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "文件不存在")
	}
	if objType(info.Name, false) != models.MediaVideo {
		return helpers.Fail(c, helpers.CodeBadRequest, "仅视频支持 FFmpeg 截图")
	}

	sha1, _, _ := mediaExtraInfo(drv, rel)
	if sha1 == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请先执行索引生成 .index.jsonl")
	}
	posterRel, err := mediaindex.PosterRel(sha1)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "无法定位封面路径")
	}

	src, _, serr := drv.Open(rel)
	if serr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "打开文件失败: "+serr.Error())
	}
	defer func() { _ = src.(interface{ Close() error }).Close() }()

	shot, ferr := ffmpegShot(src, localRealPath(drv, rel), sec)
	if ferr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, ferr.Error())
	}
	// 校验抽到的确实是可解码图片，避免把 ffmpeg 的错误输出当封面落盘
	if _, _, derr := image.Decode(bytes.NewReader(shot)); derr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "截图输出不是有效图片")
	}
	if werr := writePosterBytes(drv, posterRel, shot); werr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "写入失败: "+werr.Error())
	}
	return helpers.OK(c, nil)
}

// FsRename 同目录改名（管理员）。
func FsRename(c *echo.Context) error {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" || req.Name == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	drv, rel, err := openStorage(req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	defer func() { _ = drv.Close() }()

	if err := drv.Rename(rel, req.Name); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "改名失败: "+err.Error())
	}
	return helpers.OK(c, nil)
}

// FsMove 移动到同存储的另一个目录（管理员）。
//
// 移动是「同一存储内的 rename」，跨存储没有廉价实现（需要整份复制再删），
// 所以这里直接拦掉，避免用户以为移动成功、实际是慢速拷贝。
func FsMove(c *echo.Context) error {
	var req struct {
		Path   string `json:"path"`
		DstDir string `json:"dst_dir"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" || req.DstDir == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	srcSt, srcRel, err := resolveStorage(req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	dstSt, dstRel, err := resolveStorage(req.DstDir)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	if srcSt.ID != dstSt.ID {
		return helpers.Fail(c, helpers.CodeBadRequest, "不支持跨存储移动")
	}

	drv, err := drivers.Open(srcSt)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	if err := drv.Move(srcRel, dstRel); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "移动失败: "+err.Error())
	}
	return helpers.OK(c, nil)
}

// downloadPath 取通配段。echo v5 的 /d/*path 按实现不同，
// 通配段可能落在 "path" 或 "*" 上，这里都试一遍，不绑死某个版本细节。
func downloadPath(c *echo.Context) string {
	for _, key := range []string{"path", "*", "path*"} {
		if v := c.Param(key); v != "" {
			return v
		}
	}
	return ""
}

// Download 播放/下载实际文件，鉴权由 routes 上的 StreamAuth 负责。
//
// 注意这里的失败一律走 FailStatus（回真实 HTTP 状态码）：本接口的响应体
// 会被播放器直接当成媒体字节流解析，若失败也回 HTTP 200 + JSON 信封，
// 播放器只会报「格式不支持」这类与真实原因无关的错。
func Download(c *echo.Context) error {
	path := downloadPath(c)
	if path == "" {
		return helpers.FailStatus(c, helpers.CodeBadRequest, "路径为空")
	}
	scoped, err := scopePath(c, path)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeUnauthorized, err.Error())
	}
	// 取流同样受目录密码保护；播放器带不了自定义体，所以从查询串取密码
	if err := requireFolderPassword(scoped, c.QueryParam("password")); err != nil {
		return helpers.FailStatus(c, helpers.CodeForbidden, err.Error())
	}
	drv, rel, err := openStorage(scoped)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeNotFound, err.Error())
	}
	defer func() { _ = drv.Close() }()

	f, _, err := drv.Open(rel)
	if err != nil {
		return helpers.FailStatus(c, helpers.CodeNotFound, "文件不存在")
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()

	// ServeContent 支持 Range 请求：播放器拖动进度条、断点续传都靠它。
	http.ServeContent(c.Response(), c.Request(), filepath.Base(path), time.Now(), f)
	return nil
}
