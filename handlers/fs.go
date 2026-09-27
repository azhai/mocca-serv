package handlers

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
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
	// Summary 一两行简介（分页网格卡片底部显示）。来自该文件 .mocca 附加信息的 summary；
	// 分页时只对当前页条目读，避免整目录逐文件读 .mocca。
	Summary string `json:"summary,omitempty"`
	// Director/Year/Cast 同 .mocca 附加信息：网格卡片标题下展示导演·年份与主演。
	Director string   `json:"director,omitempty"`
	Year     int      `json:"year,omitempty"`
	Cast     []string `json:"cast,omitempty"`
}

// MediaDetail 对象详情：在列表条目基础上追加播放地址。
//
// Header 是 APP `parsedHeaders()` 要的多行 `Key: Value` 字符串，**不是 map**：
// 写成 map 会让 APP 侧解析失败。Provider 同理，缺失时 APP 取默认值。
type MediaDetail struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Type     int    `json:"type"`
	Thumb    string `json:"thumb"`
	Sign     string `json:"sign"`
	Modified string `json:"modified"`
	RawURL   string `json:"raw_url"`
	// HLSURL 旁路（sidecar）m3u8 清单地址，可选：同目录隐藏子目录里已有清单时才非空。
	// 服务端只把清单文本当普通文件发给客户端，分片同理；生成在服务端之外，不在请求时切片。
	HLSURL   string   `json:"hls_url,omitempty"`
	Header   string   `json:"header"`
	Provider string   `json:"provider"`
	Title    string   `json:"title,omitempty"`
	Duration int      `json:"duration,omitempty"`
	Cover    string   `json:"cover,omitempty"`
	Desc     string   `json:"summary,omitempty"`
	Authors  []string `json:"authors,omitempty"`
}

// resolveStorage 按挂载点最长匹配选中存储，并算出存储内的相对路径。
// 额外返回存储本身，供「跨存储移动」这类需要比对来源与目标的场景使用。
// 匹配与裁剪一律用 NormalizeMountPath 后的挂载点：库里若存了不规范写法
// （尾斜杠、双斜杠），与 FsList 的 resolveStorageStrict 保持同一口径，
// 否则会出现「列表能看、/fs/get 却 404」的路径错位。
// 根挂载点（/）单独放行：它匹配任意路径，否则 HasPrefix(x, "//") 恒假，
// 根挂载下的文件会全被 fallback 错误路由到别的存储，导致取流 404。
func resolveStorage(reqPath string) (*models.Storage, string, error) {
	reqPath = ensureLeadingSlash(reqPath)

	storages, err := models.ListStorages()
	if err != nil {
		return nil, "", errors.WithStack(err)
	}
	var matched, fallback *models.Storage
	var matchedMount, fallbackMount string
	for _, s := range storages {
		if s.Disabled {
			continue
		}
		mount := models.NormalizeMountPath(s.MountPath)
		if fallback == nil || len(mount) > len(fallbackMount) {
			fallback, fallbackMount = s, mount
		}
		if mount == "/" || reqPath == mount || strings.HasPrefix(reqPath, mount+"/") {
			if matched == nil || len(mount) > len(matchedMount) {
				matched, matchedMount = s, mount
			}
		}
	}
	target, targetMount := matched, matchedMount
	if target == nil {
		target, targetMount = fallback, fallbackMount
	}
	if target == nil {
		return nil, "", errors.New("尚未配置存储，请先添加挂载点")
	}

	rel := strings.TrimPrefix(reqPath, targetMount)
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
//     分支）、.git，以及设备侧的元数据根 .mocca（封面、简介、索引，
//     它就在媒体库旁，不挡会出现在浏览列表里）；
//  2. 完整名字命中 systemReservedNames（不区分大小写），如盘根的 $RECYCLE.BIN。
//
// 除此之外不按字符做任何判断：名字里凡是不构成上述两条的符号一律保留。
//
// 只按名字判断，不看隐藏属性位：Local 与 SMB 对隐藏属性的语义并不一致。
// 另外过滤只作用于「列表」，按显式路径取流（/d/media/.mocca/ab/cd/xxxx.png）
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
	case ".txt", ".md", ".srt", ".ass", ".vtt", ".json", ".nfo":
		return models.MediaText
	}
	// 图片/音频/视频统一走 mediaindex.MediaKindOf：**列表里认得出的，索引就一定会收录**。
	// 这里曾经自带一份更宽的清单，与索引收录用的扩展名清单不一致，于是
	// .mkv 这类文件能显示、却进不了索引（拿不到 sha1 → 海报一直 404）。
	return mediaindex.MediaKindOf(name)
}

// hlsAssetExts 旁路 HLS 自身的扩展名：清单与分片不该再回头找「自己的清单」。
var hlsAssetExts = map[string]bool{".m3u8": true, ".ts": true, ".m4s": true}

// isHLSAsset 判断是不是旁路 HLS 的清单/分片文件。
func isHLSAsset(name string) bool {
	return hlsAssetExts[strings.ToLower(filepath.Ext(name))]
}

// streamContentTypes 取流时要钉死的内容类型。
//
// Go 内置 mime 表**没有** .m3u8/.ts 映射（只有系统装了 /etc/apache2/mime.types
// 之类才靠它兜底），而清单若不是 application/vnd.apple.mpegurl，Safari 原生 HLS
// 会直接拒播。所以这里不赌运行环境，按扩展名显式指定。
var streamContentTypes = map[string]string{
	".m3u8": "application/vnd.apple.mpegurl",
	".ts":   "video/mp2t",
	".m4s":  "video/iso.segment",
	".vtt":  "text/vtt",
}

// setStreamContentType 在 http.ServeContent 之前把内容类型定好：
// ServeContent 只在响应头还没有 Content-Type 时才去按扩展名/嗅探猜，先设好它就照用。
func setStreamContentType(h http.Header, name string) {
	if ct, ok := streamContentTypes[strings.ToLower(filepath.Ext(name))]; ok {
		h.Set("Content-Type", ct)
	}
}

// setNoStoreIfCredentialed 给「带了凭证」的取流响应打上不许缓存的指令。
//
// 为什么：/d 的地址上可能就挂着凭证（?token= 或受保护目录的 ?password=），
// 也可能由 Authorization 头携带。这种响应一旦被共享缓存/代理存下来，
// 等于把内容连同凭证一起留在了中间节点上。
// 不带任何凭证的请求（默认的游客浏览）不加这条指令，保持原来的可缓存行为。
//
// 注意 no-store 也会让浏览器不缓存这段流：来回拖进度条会重新发请求。
// 这是刻意的取舍 —— 带凭证的内容优先保证不落缓存。
func setNoStoreIfCredentialed(c *echo.Context) {
	req := c.Request()
	if req.Header.Get("Authorization") == "" &&
		c.QueryParam("token") == "" && c.QueryParam("password") == "" {
		return
	}
	c.Response().Header().Set("Cache-Control", "private, no-store")
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

	objs, total, err := listDir(models.NormalizeMountPath(scoped), req.Page, req.PerPage)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	// total 取过滤后的条数，与 content 保持一致（分页时是媒体总数）。
	// is_mount 告诉客户端「这个目录自己是不是一个挂载点」——根没被挂存储时
	// 它只是聚合树的虚拟根，根被挂了存储（mount_path=/）时它是挂载点。
	return helpers.OK(c, map[string]any{
		"content": objs, "total": total,
		"is_mount": models.IsMount(scoped),
		"page":     req.Page, "per_page": req.PerPage,
	})
}

// listDir 组装一层目录的条目：挂载点在最前，其后是排序过的真实条目。
// 传入 page/perPage（>=1）时按 .index.jsonl 行区间返回**仅媒体**的分页页（网格用）：
// 子目录/文本不进分页，靠左侧目录树+面包屑导航；返回的 total 为该层媒体总数。
// 否则返回旧的整目录列表（含子目录/文本，兼容列表视图）。
func listDir(dir string, page, perPage int) ([]MediaObj, int, error) {
	if page >= 1 && perPage >= 1 {
		return listDirPages(dir, page, perPage)
	}
	return listDirFull(dir)
}

// listDirPages 分页列表：只列媒体，按 .index.jsonl 行区间切，逐条读 .mocca 简介。
func listDirPages(dir string, page, perPage int) ([]MediaObj, int, error) {
	st, rel, err := resolveStorageStrict(dir)
	if err != nil {
		return nil, 0, err
	}
	drv, err := drivers.Open(st)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = drv.Close() }()

	idxRel := strings.TrimSuffix(rel, "/") + "/" + mediaindex.IndexFileName
	f, _, err := drv.Open(idxRel)
	if err != nil {
		return nil, 0, nil // 无索引：空媒体页
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()

	rows, total, err := mediaindex.ReadIndexPage(f, (page-1)*perPage, perPage)
	if err != nil {
		return nil, 0, err
	}

	// 名字 → 真实条目，取大小/修改时间/类型；已被删除的行跳过。
	names := make(map[string]drivers.Entry)
	if entries, lerr := drv.List(rel); lerr == nil {
		for _, e := range entries {
			if !isExcludedName(e.Name) {
				names[e.Name] = e
			}
		}
	}

	out := make([]MediaObj, 0, len(rows))
	for _, r := range rows {
		e, ok := names[r.Name]
		if !ok {
			continue
		}
		o := MediaObj{
			Name: e.Name, Size: e.Size, IsDir: false,
			Modified: e.Modified.Format(time.RFC3339),
			Type:     objType(e.Name, false), SHA1: r.SHA1,
		}
		if sd, serr := readSummaryJSON(drv, r.SHA1); serr == nil && sd != nil {
			o.Summary = sd.Summary
			o.Director = sd.Director
			o.Year = sd.Year
			o.Cast = sd.Cast
		}
		out = append(out, o)
	}
	return out, total, nil
}

// listDirFull 完整列表：挂载点 + 排序后的真实条目（含子目录/文本）。
func listDirFull(dir string) ([]MediaObj, int, error) {
	// 先读真实条目，才能知道这一层哪些名字会与挂载点重名
	entries, err := realEntries(dir, len(models.MountChildren(dir)) > 0)
	if err != nil {
		return nil, 0, err
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

	// 挂载点已按名字排好（聚合树保证），真实条目接在后面。
	// total 与 content 一致：完整列表的条数 = 挂载点 + 真实条目（分页语义下才是媒体总数）。
	return append(mountObjs, rest...), len(rest) + len(mountObjs), nil
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

	// 旁路 HLS：同目录隐藏子目录里若已有外部队列好的清单，就把它的地址一并发出去，
	// 客户端据此可以改走 m3u8（弱网下能按分片续拉）。没有清单就只回 raw_url。
	if !info.IsDir && !isHLSAsset(info.Name) && objType(info.Name, false) == models.MediaVideo {
		if hlsRel := mediaindex.HLSPlaylistRel(rel); hlsRel != "" {
			if _, serr := drv.Stat(hlsRel); serr == nil {
				detail.HLSURL = rawURL(c, mediaindex.HLSPlaylistPath(req.Path))
			}
		}
	}

	// 附加信息走设备 .mocca/<sha1>.json，是可选增强：没有就只回基础字段
	if sha1hex, _, posterOK := mediaExtraInfo(drv, rel); sha1hex != "" {
		if sd, err := readSummaryJSON(drv, sha1hex); err == nil && sd != nil {
			detail.Desc = sd.Summary
			for _, ns := range [][]string{sd.Cast, sd.Lead, sd.Backing, sd.Instrument} {
				detail.Authors = append(detail.Authors, ns...)
			}
		}
		// 封面：海报**相对 meta_dir 的路径**（形如 xx/xx/<sha1>.png）。它已经是
		// 统一处理过的 400×300 高压缩 PNG（上传/截图/刮削三条路都走 ResizeCoverPNG）。
		// 要显示请用 /meta/poster?path= 而不是拼这个路径。
		if posterOK {
			if pr, err := mediaindex.PosterRel(sha1hex); err == nil {
				detail.Cover = filepath.ToSlash(pr)
			}
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
		if _, serr := drv.MetaStat(posterRel); serr == nil {
			posterOK = true
		}
	}
	if sumRel, err := mediaindex.SummaryRel(sha1); err == nil {
		if sf, _, serr := drv.MetaOpen(sumRel); serr == nil {
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
	// 附加信息以设备 .mocca/<sha1>.json 为唯一来源，逐字段回给前端。
	// 无 sha1 或读不到 JSON 时 meta 保持 null，前端按「无附加信息」处理。
	if sd, err := readSummaryJSON(drv, sha1); err == nil && sd != nil {
		resp["meta"] = map[string]any{
			"summary":  sd.Summary,
			"director": sd.Director, "year": sd.Year,
			"region": sd.Region, "studio": sd.Studio, "language": sd.Language,
			"cast": sd.Cast, "lead": sd.Lead,
			"backing": sd.Backing, "instrument": sd.Instrument,
		}
	}
	return helpers.OK(c, resp)
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
	f, _, err := drv.MetaOpen(posterRel)
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
		Path       string   `json:"path"`
		Name       string   `json:"name"` // 不含扩展名的新文件名；空或未变则不移动
		Summary    string   `json:"summary"`
		Director   string   `json:"director"`
		Cast       []string `json:"cast"` // 主演（视频）
		Year       int      `json:"year"`
		Region     string   `json:"region"`
		Studio     string   `json:"studio"`
		Language   string   `json:"language"`   // 语言（音频）
		Lead       []string `json:"lead"`       // 主唱（音频）
		Backing    []string `json:"backing"`    // 伴唱（音频）
		Instrument []string `json:"instrument"` // 演奏（音频）
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

	// 附加信息要落到本存储的 .mocca/…/<sha1>.json；改名不改变内容哈希，所以
	// 先在命名未变时按原名在 .index.jsonl 里取一次 sha1（改名后那里找不到了）。
	sha1hex, _, _ := mediaExtraInfo(drv, rel)

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
	kind := objType(info.Name, false)
	if !models.IsValidMediaKind(kind) {
		return helpers.OK(c, map[string]any{"path": newPath, "name": info.Name})
	}

	// 视频/音频的附加信息持久化到移动设备上的 .mocca/xx/xx/<sha1>.json（图库
	// 只留账号/挂载点这类轻量数据）；.index.jsonl 没记 sha1 就现场整读算一次。
	// 图片无附加信息，不写。目录 `.mocca/xx/xx/` 不存在则先建。
	if kind == models.MediaVideo || kind == models.MediaAudio {
		if sha1hex == "" {
			sha1hex, _ = sha1OfRel(drv, rel)
		}
		if sha1hex != "" {
			sd := summaryData{
				Summary:  req.Summary,
				Director: req.Director, Year: req.Year,
				Region: req.Region, Studio: req.Studio, Language: req.Language,
				Cast: req.Cast, Lead: req.Lead,
				Backing: req.Backing, Instrument: req.Instrument,
			}
			if err := writeSummaryJSON(drv, sha1hex, sd); err != nil {
				return helpers.Fail(c, helpers.CodeInternal, "保存附加信息失败: "+err.Error())
			}
		}
	}
	return helpers.OK(c, map[string]any{"path": newPath, "name": info.Name})
}

// stripExt 去文件名扩展名（.mp4 之类）。无扩展名原样返回。
func stripExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// summaryData 附加信息落盘到 .mocca 的 JSON 结构。设备侧这份是权威持久化，
// 重启/换盘不丢；同一份内容不再镜像到 sqlite（见 readSummaryJSON / FsInfo）。
type summaryData struct {
	Summary    string   `json:"summary"`
	Director   string   `json:"director,omitempty"`
	Year       int      `json:"year,omitempty"`
	Region     string   `json:"region,omitempty"`
	Studio     string   `json:"studio,omitempty"`
	Language   string   `json:"language,omitempty"`
	Cast       []string `json:"cast,omitempty"`
	Lead       []string `json:"lead,omitempty"`
	Backing    []string `json:"backing,omitempty"`
	Instrument []string `json:"instrument,omitempty"`
}

// writeSummaryJSON 把附加信息写到 `.mocca/xx/xx/<sha1>.meta`，目录不存在先建。
func writeSummaryJSON(drv drivers.Driver, sha1hex string, sd summaryData) error {
	rel, err := mediaindex.SummaryRel(sha1hex)
	if err != nil {
		return err
	}
	if err := drv.MetaMkdirAll(path.Dir(rel)); err != nil {
		return errors.Wrapf(err, "创建 %s 失败", path.Dir(rel))
	}
	b, err := json.MarshalIndent(sd, "", "  ")
	if err != nil {
		return err
	}
	f, err := drv.MetaCreate(rel)
	if err != nil {
		return errors.Wrapf(err, "写 %s 失败", rel)
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	if _, err := f.Write(b); err != nil {
		return errors.Wrapf(err, "写 %s 失败", rel)
	}
	return nil
}

// readSummaryJSON 读 `.mocca/xx/xx/<sha1>.json` 的媒体附加信息。
// 文件不存在或 JSON 非法都返回 (nil, 错误)；调用方按「无附加信息」处理。
// 这是设备侧的权威来源：改资料、重启、换盘都不经过数据库。
func readSummaryJSON(drv drivers.Driver, sha1hex string) (*summaryData, error) {
	rel, err := mediaindex.SummaryRel(sha1hex)
	if err != nil {
		return nil, err
	}
	f, _, err := drv.MetaOpen(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var sd summaryData
	if err := json.Unmarshal(b, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

// sha1OfRel 整读一个媒体文件算内容 sha1（.index.jsonl 没有记录时兜底用）。
func sha1OfRel(drv drivers.Driver, rel string) (string, error) {
	f, _, err := drv.Open(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
	if _, _, derr := image.Decode(bytes.NewReader(data)); derr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "不是有效的图片文件")
	}
	// 统一转成 400×300 裁剪填满的高压缩 PNG 再落盘
	png, perr := mediaindex.ResizeCoverPNG(data)
	if perr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "封面处理失败")
	}
	if werr := writePosterBytes(drv, posterRel, png); werr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "写入失败: "+werr.Error())
	}
	return helpers.OK(c, nil)
}

// FsUncov 删除某个音/视频的封面（管理员）。
//
// 与「上传封面」「FFmpeg 截图」相对：那两条路是**替换**封面，这条是把封面清掉、
// 回到"没有封面"的状态（之后重新刮削就会重新取 TMDB 海报，因为海报文件已不存在）。
// 同样依赖 .index.jsonl 定位 sha1 —— 封面按 sha1 寻址。
func FsUncov(c *echo.Context) error {
	var req GetReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		req.Path = c.QueryParam("path")
	}
	if req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	stor, rel, err := resolveStorageStrict(req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	sha1, _, posterOK := mediaExtraInfo(drv, rel)
	if sha1 == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请先执行索引生成 .index.jsonl")
	}
	if !posterOK {
		return helpers.OK(c, map[string]any{"path": req.Path, "removed": false}) // 本来就没有，幂等
	}
	posterRel, err := mediaindex.PosterRel(sha1)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "无法定位封面路径")
	}
	if rerr := drv.MetaRemove(posterRel); rerr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "删除封面失败: "+rerr.Error())
	}
	// 封面没了 → 重扫一层让 is_new 翻回 1（列表重新标成"待补封面"）。
	// 失败不阻断：下次扫描仍会修正它。
	_ = mediaindex.ScanDir(drv, path.Dir(rel))
	return helpers.OK(c, map[string]any{"path": req.Path, "removed": true})
}

// writePosterBytes 把已校验的图片字节写入某文件的 .mocca 海报位置，幂等覆盖旧图。
// 校验由各调用方各自负责（上传前的图片解码 / 截图后的输出校验）。
func writePosterBytes(drv drivers.Driver, posterRel string, data []byte) error {
	w, err := drv.MetaCreate(posterRel)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	_, err = w.Write(data)
	return err
}

// FsShot 用 FFmpeg 按指定秒数从视频抽一帧，写入 .mocca 封面位置（管理员）。
// 与 FsCov 一样依赖 .index.jsonl 定位 sha1；入参 path + sec（秒，可小数）。
// ShotReq FFmpeg 截图请求：前端以 JSON body 提交（path + sec）。
// query/form 形式保留兼容旧调用方。sec 既可是纯秒数（数字或字符串），
// 也可是「时:分:秒」形式（如 1:30 或 1:02:30），见 shotSecOf/normalizeShotSec。
type ShotReq struct {
	Path string `json:"path"`
	Sec  any    `json:"sec"`
}

// shotSecOf 把 ShotReq.Sec 里各种合法编码统一成字符串：
// JSON 数字（encoding/json 给 float64）、字符串、json.Number 都能接住。
func shotSecOf(v any) string {
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	}
	return ""
}

// normalizeShotSec 归一化截图时间点给 ffmpeg -ss 用。空值按第 1 秒。
//   - 纯秒（含小数）→ 原样（小于 1 统一为 1）
//   - HH:MM[:SS] → 原样（ffmpeg 原生支持冒号时分秒）
//
// 都不是 → 返回空串，由调用方报「时间无效」。
func normalizeShotSec(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "1"
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if f < 1 {
			return "1"
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return ""
	}
	for _, p := range parts {
		if _, err := strconv.ParseFloat(p, 64); err != nil {
			return ""
		}
	}
	return s
}

func FsShot(c *echo.Context) error {
	var req ShotReq
	// JSON body 优先；query/form 兜底（旧调用方/测试用 ?path= 与 ?sec=）
	if err := c.Bind(&req); err != nil || req.Path == "" {
		req.Path = c.QueryParam("path")
		if req.Path == "" {
			req.Path = c.FormValue("path")
		}
	}
	if req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	sec := shotSecOf(req.Sec)
	if sec == "" {
		sec = c.QueryParam("sec")
	}
	if sec == "" {
		sec = c.FormValue("sec")
	}
	sec = normalizeShotSec(sec)
	if sec == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "截图时间无效，可用秒数或「时:分:秒」")
	}

	stor, rel, err := resolveStorageStrict(req.Path)
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

	raw, ferr := mediaindex.FFmpegShot(drv, rel, sec)
	if ferr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, ferr.Error())
	}
	// 校验抽到的确实是可解码图片，避免把 ffmpeg 的错误输出当封面落盘
	if _, _, derr := image.Decode(bytes.NewReader(raw)); derr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "截图输出不是有效图片")
	}
	// 统一转成 400×300 裁剪填满的高压缩 PNG 再落盘
	png, perr := mediaindex.ResizeCoverPNG(raw)
	if perr != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "封面处理失败")
	}
	if werr := writePosterBytes(drv, posterRel, png); werr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "写入失败: "+werr.Error())
	}
	return helpers.OK(c, nil)
}

// PatchReq 目录级「补充截图」请求：对该目录下所有**缺封面**的视频
// 在指定时间点抽帧生成封面（管理员）。sec 同 FsShot，秒数或「时:分:秒」。
type PatchReq struct {
	Path string `json:"path"`
	Sec  any    `json:"sec"`
}

// FsPatch 目录级补充截图：启动扫描不再自动抽封面，缺封面的视频在这里集中补齐。
// 遍历该目录 .index.jsonl，跳过已有封面的，给仍缺封面的视频调 ffmpeg 并写 .mocca 海报。
func FsPatch(c *echo.Context) error {
	var req PatchReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		req.Path = c.QueryParam("path")
		if req.Path == "" {
			req.Path = c.FormValue("path")
		}
	}
	if req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	sec := shotSecOf(req.Sec)
	if sec == "" {
		sec = c.QueryParam("sec")
	}
	if sec == "" {
		sec = c.FormValue("sec")
	}
	sec = normalizeShotSec(sec)
	if sec == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "截图时间无效，可用秒数或「时:分:秒」")
	}

	st, rel, err := resolveStorageStrict(req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, err.Error())
	}
	drv, err := drivers.Open(st)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	defer func() { _ = drv.Close() }()

	// 目录层自身可能是聚合树上的中间节点，得是真实存储里的目录才有索引
	idxRel := strings.TrimSuffix(rel, "/") + "/" + mediaindex.IndexFileName
	f, _, err := drv.Open(idxRel)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "该目录还没有 .index.jsonl，请先索引")
	}
	rows, _, err := mediaindex.ReadIndexPage(f, 0, 0)
	_ = f.(interface{ Close() error }).Close()
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取索引失败: "+err.Error())
	}

	dirBase := strings.TrimSuffix(rel, "/")
	// dirJoin 拼接「目录内某文件」的存储相对路径；根目录（""）时直接带前导斜杠。
	dirJoin := func(name string) string {
		if dirBase == "" {
			return "/" + name
		}
		return dirBase + "/" + name
	}

	done := 0
	covered := 0
	failed := 0
	for _, rec := range rows {
		if guessType(rec.Name) != models.MediaVideo || rec.SHA1 == "" {
			continue
		}
		covered++
		posterRel, perr := mediaindex.PosterRel(rec.SHA1)
		if perr != nil { // 理论上只有 sha1 非合法才发生
			failed++
			continue
		}
		if _, serr := drv.MetaStat(posterRel); serr == nil {
			continue // 已有封面，跳过
		}
		shotRel := dirJoin(rec.Name)
		raw, ferr := mediaindex.FFmpegShot(drv, shotRel, sec)
		if ferr != nil {
			failed++
			continue
		}
		if _, _, derr := image.Decode(bytes.NewReader(raw)); derr != nil {
			failed++
			continue
		}
		png, perr := mediaindex.ResizeCoverPNG(raw)
		if perr != nil {
			failed++
			continue
		}
		if werr := mediaindex.WritePoster(drv, rec.SHA1, png); werr != nil {
			failed++
			continue
		}
		done++
	}
	return helpers.OK(c, map[string]any{
		"done": done, "covered": covered, "failed": failed, "sec": sec,
	})
}

// HlsReq 视频切分请求：把一个视频切成旁路 HLS（清单 + 分片）。
type HlsReq struct {
	Path  string `json:"path"`
	Force bool   `json:"force"` // 已有清单时是否重切
}

// FsHLS 把一个视频切成旁路 HLS（管理员）。
//
// 只切不转（ffmpeg -c copy）：耗时与 CPU 都低，画质与原文件一致，产物写进
// <目录>/.hls/<文件名>/（点开头，不进列表、不被索引，但 /d 可取流）。之后
// /fs/get 会给该视频带上 hls_url，浏览应用就会**优先走 HLS**，没有清单时仍走 Range 直链。
//
// 一次只处理一个文件：后台「视频切分」页对多选逐个发请求，各自报进度，
// 某个失败也不会牵连其余。已有的清单默认跳过，force=true 才重切。
func FsHLS(c *echo.Context) error {
	var req HlsReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		req.Path = c.QueryParam("path")
		if req.Path == "" {
			req.Path = c.FormValue("path")
		}
	}
	if req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}

	stor, rel, err := resolveStorageStrict(req.Path)
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
	if info.IsDir || objType(info.Name, false) != models.MediaVideo || isHLSAsset(info.Name) {
		return helpers.Fail(c, helpers.CodeBadRequest, "仅视频文件支持切分为 HLS")
	}

	_, skipped, err := mediaindex.HLSSegment(drv, rel, req.Force)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	return helpers.OK(c, map[string]any{
		"path":     req.Path,
		"playlist": mediaindex.HLSPlaylistPath(req.Path),
		"skipped":  skipped,
	})
}

// ReindexReq 重新索引请求：按增量重建选中条目所在目录的 .index.jsonl
// （索引按目录一份，写入必然是整份；是否重算 sha1 由 size+modified 比对决定）。
type ReindexReq struct {
	Path  string   `json:"path"`  // 单路径（兼容脚本调用）
	Paths []string `json:"paths"` // 批量（后台「视频切分」页多选）
}

// FsReindex 重新索引（管理员）：**按增量重建**选中条目所在目录的 `.index.jsonl`。
//
// 语义就是"这个目录的索引，按当前实际情况重建一遍"，代价全在判据上：逐条比
// **name + size + modified** ——
//
//   - 与索引里那条一致 → 直接沿用旧行（sha1 与已提取的元数据照抄，**不读文件内容**）；
//   - 不一致 / 没有旧行 → 整读一遍算 sha1，并重新解析元数据。
//
// 所以**勾选只决定"刷新哪些目录"**，不决定重算哪些文件 —— 这一点被改过两次：
// 早先对选中项所在目录**整层强制重算**，勾 1 个文件也要把同目录几十个视频全部重读
// （20 个 4GB 的片子 = 80GB I/O），点一次「重新索引」要等很久；再后来改成"只强制点名的"，
// 但那仍然无视了一个事实——**文件变没变，size+modified 一比就知道**，不该无条件重读。
//
// 想整层强制重算（同名同大小、mtime 也被保留、内容却被换过的极少数情况）：
// 走 mediaindex.RebuildFiles 的 force 参数，本接口不暴露（正常路径不需要）。
//
// 多选时按目录归拢：同一个目录只重写一次索引（写入在 mediaindex 里另有 indexMu 串行化，
// 这里不依赖它防重复）。回包 `files` = 写进索引的记录数，`rehashed` = 其中真正重算了 sha1 的条数。
func FsReindex(c *echo.Context) error {
	var req ReindexReq
	if err := c.Bind(&req); err != nil {
		req.Path = c.QueryParam("path")
	}
	paths := req.Paths
	if req.Path != "" {
		paths = append(paths, req.Path)
	}
	if len(paths) == 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "请先选择要重新索引的文件")
	}

	// 按存储分组：一批勾选可能跨挂载点，每个存储要单独开驱动。
	storOf := map[string]*models.Storage{}
	relsOf := map[string][]string{}
	var mounts []string
	for _, p := range paths {
		stor, rel, err := resolveStorageStrict(p)
		if err != nil {
			return helpers.Fail(c, helpers.CodeNotFound, err.Error())
		}
		if _, ok := relsOf[stor.MountPath]; !ok {
			mounts = append(mounts, stor.MountPath)
			storOf[stor.MountPath] = stor
		}
		relsOf[stor.MountPath] = append(relsOf[stor.MountPath], rel)
	}

	dirs, files, rehashed := 0, 0, 0
	for _, mount := range mounts {
		err := func() error {
			drv, oerr := drivers.Open(storOf[mount])
			if oerr != nil {
				return oerr
			}
			defer func() { _ = drv.Close() }()

			// 勾选只用来定"刷新哪些目录"：索引按目录一份，同一个目录只重写一次。
			var order []string
			seen := map[string]bool{}
			for _, rel := range relsOf[mount] {
				// 先 Stat：一是要区分"传进来的是目录"（刷新它本身），
				// 二是不存在时必须明确报错 —— 早先这里失败会静默退到 path.Dir，
				// 结果写错一个文件名就去把它的父目录重建了一遍，非常难发现。
				info, serr := drv.Stat(rel)
				if serr != nil {
					return errors.Wrapf(errReindexMissing, "%s 不存在", rel)
				}
				dir := path.Dir(rel)
				if info.IsDir {
					dir = rel
				}
				if seen[dir] {
					continue // 同一目录只重写一次索引
				}
				seen[dir] = true
				order = append(order, dir)
			}

			for _, dir := range order {
				// force 传空：重建完全按增量判据（size+modified 与旧行比），
				// 文件没变就不重读 —— 这就是"做增量的重建"。
				written, n, rerr := mediaindex.RebuildFiles(drv, dir, nil)
				if rerr != nil {
					return errors.Wrapf(rerr, "重建 %s 的索引失败", dir)
				}
				dirs++
				files += written
				rehashed += n
			}
			return nil
		}()
		if errors.Is(err, errReindexMissing) {
			return helpers.Fail(c, helpers.CodeNotFound, err.Error())
		}
		if err != nil {
			return helpers.Fail(c, helpers.CodeInternal, err.Error())
		}
	}
	return helpers.OK(c, map[string]any{"dirs": dirs, "files": files, "rehashed": rehashed})
}

// errReindexMissing 重新索引时某个路径不存在。单独一个哨兵值，好把它映射成 404
// 而不是笼统的 500 —— 这是调用方能自己修的错。
var errReindexMissing = errors.New("路径不存在")

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
	// 放在最前面：handler 自己产出的失败响应（如文件不存在的 404）也要带上这条指令。
	// 404 正属于 RFC 9111 里「默认可被启发式缓存」的状态码，不标就会留在中间节点上。
	// （令牌无效时由 StreamAuth 提前回 401，根本走不到这里；401 不在那个清单里。）
	setNoStoreIfCredentialed(c)

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

	// 旁路 HLS 的清单/分片要按标准类型发出（m3u8/ts 不赌 Go 的内置 mime 表），
	// 其余仍交给 ServeContent 按扩展名/嗅探决定。
	setStreamContentType(c.Response().Header(), filepath.Base(path))

	// ServeContent 支持 Range 请求：播放器拖动进度条、断点续传都靠它。
	// 直链（无旁路清单时的默认路径）走的就是这里。
	http.ServeContent(c.Response(), c.Request(), filepath.Base(path), time.Now(), f)
	return nil
}
