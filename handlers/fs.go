package handlers

import (
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/helpers"
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
	// Sign 受保护路径的签名。当前用「目录密码 + 令牌」保护，
	// 故恒为空；保留字段是为了让 APP 的 `sign` 解析路径始终有值可选。
	Sign string `json:"sign"`
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
	req.Path = scoped

	// 目录密码先于存储访问校验，避免未授权就触碰后端
	if err := requireFolderPassword(req.Path, req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeForbidden, err.Error())
	}

	drv, rel, err := openStorage(req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	defer func() { _ = drv.Close() }()

	entries, err := drv.List(rel)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "目录不存在")
	}
	// 首字符特殊的条目直接剔除，不做任何重排：顺序仍是驱动返回的原始顺序。
	// 用 append 而非预分配下标，是为了「只跳过、不移动」。
	objs := make([]MediaObj, 0, len(entries))
	for _, e := range entries {
		if isExcludedName(e.Name) {
			continue
		}
		objs = append(objs, MediaObj{
			Name:     e.Name,
			Size:     e.Size,
			IsDir:    e.IsDir,
			Modified: e.Modified.Format(time.RFC3339),
			Type:     objType(e.Name, e.IsDir),
		})
	}
	// total 取过滤后的条数，与 content 保持一致
	return helpers.OK(c, map[string]any{"content": objs, "total": len(objs)})
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
