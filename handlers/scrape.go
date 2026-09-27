package handlers

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
	"github.com/azhai/mocca/tmdb"
	"github.com/labstack/echo/v5"
)

// 刮削（scrape）分两步走，与「先检索、再选一条」的实际操作一致：
//
//	POST /api/fs/scrape        按片名（可从文件名猜）出候选列表
//	POST /api/fs/scrape/apply  把选中的那条写进设备侧 .mocca 附加信息与封面
//
// 两条都只给管理员（routes 里挂在 admin 组）：它们既调外部网络又写用户磁盘。
// 与 /fs/cov、/fs/shot 一样不做目录密码校验——这两件事都是管理员自己的操作。
//
// 只在设备侧落盘，不碰 sqlite：附加信息的唯一权威来源仍是
// `.mocca/xx/xx/<sha1>.meta`（文字）与 `<sha1>.png`（封面），见 readSummaryJSON。

// ScrapeReq 检索请求。
type ScrapeReq struct {
	Path string `json:"path"`
	// Keyword 留空则按文件名猜；填写时可以是「片名 + 年份」（`无间道 2002`、`无间道-2002`），
	// 末尾的年份会被拆出来当过滤条件（见 tmdb.SplitKeyword）。
	Keyword string `json:"keyword"`
	Year    int    `json:"year"` // 仅在与 Keyword 同时给出时生效，用来收窄结果
}

// ScrapeApplyReq 应用请求：把某条 TMDB 结果的详情与海报写进该文件。
type ScrapeApplyReq struct {
	Path   string `json:"path"`
	TmdbID int    `json:"tmdb_id"`
	// Keep 可选：表单里**当前已经有内容**的字段，刮削一律不覆盖它们。
	// 由前端上报，因为它才是"用户此刻看到的东西"。nil = 调用方没给（脚本/老客户端），
	// 这时退回按存储内容判断（已有内容不覆盖）。
	Keep *ScrapeKeep `json:"keep"`
}

// ScrapeKeep 表单里已经有内容的字段。判定"要不要覆盖"以**表单**为准而不是 .mocca：
// 用户在表单里把某一格清空，就是想让它被重新填上，哪怕磁盘上还留着旧值。
type ScrapeKeep struct {
	Summary  bool `json:"summary"`
	Director bool `json:"director"`
	Cast     bool `json:"cast"`
	Year     bool `json:"year"`
}

// scrapeTarget 刮削目标：打开好的驱动 + 存储内相对路径 + 文件名 + 内容 sha1。
type scrapeTarget struct {
	drv  drivers.Driver
	rel  string
	name string
	sha1 string
}

// openScrapeTarget 刮削的公共前置：路径收敛 → 定位存储 → 打开驱动 → 校验视频 → 取 sha1。
// 失败时返回业务码与可直接展示的中文原因；成功时 code 为 0。
func openScrapeTarget(scoped string) (*scrapeTarget, int, string) {
	stor, rel, err := resolveStorageStrict(scoped)
	if err != nil {
		return nil, helpers.CodeNotFound, err.Error()
	}
	drv, err := drivers.Open(stor)
	if err != nil {
		return nil, helpers.CodeInternal, err.Error()
	}
	info, err := drv.Stat(rel)
	if err != nil {
		_ = drv.Close()
		return nil, helpers.CodeNotFound, "文件不存在"
	}
	// TMDB 只有影视库：音频/图片刮不出东西，早点说清楚，别让用户等一次网络往返
	if objType(info.Name, false) != models.MediaVideo {
		_ = drv.Close()
		return nil, helpers.CodeBadRequest, "TMDB 刮削只支持视频文件"
	}
	// 附加信息按内容 sha1 寻址：索引里有就直接用，没有则整读算一次（与 FsEdit 同口径）
	sha1hex, _, _ := mediaExtraInfo(drv, rel)
	if sha1hex == "" {
		sha1hex, _ = sha1OfRel(drv, rel)
	}
	if sha1hex == "" {
		_ = drv.Close()
		return nil, helpers.CodeBadRequest, "无法定位该文件的内容 sha1"
	}
	return &scrapeTarget{drv: drv, rel: rel, name: info.Name, sha1: sha1hex}, 0, ""
}

// tmdbClient 用配置建一个 TMDB 客户端；没配密钥时返回的中文原因可直接展示。
func tmdbClient() (*tmdb.Client, error) {
	key, lang, proxy := "", "", ""
	if config.Cfg != nil {
		key, lang = config.Cfg.TmdbAPIKey, config.Cfg.TmdbLang
		proxy = config.Cfg.TmdbProxy
	}
	return tmdb.New(key, lang, proxy)
}

// ScrapeSearch 按片名检索 TMDB 电影，返回候选列表。
//
// 关键词优先用请求里给的；没给就按文件名猜。猜出来的年份只用于收窄，
// 若带年份查不到，客户端会自动去掉年份重查一次（见 tmdb.Client.Search）。
func ScrapeSearch(c *echo.Context) error {
	var req ScrapeReq
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	scoped, err := scopePath(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}
	t, code, msg := openScrapeTarget(scoped)
	if t == nil {
		return helpers.Fail(c, code, msg)
	}
	defer func() { _ = t.drv.Close() }()

	cli, err := tmdbClient()
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}

	// 关键词的来源有两种，都要把年份与片名分开：TMDB 的 year 是严格过滤参数，
	// 年份留在关键词里（`让子弹飞 2010`）几乎必然搜不到。
	keyword, year := strings.TrimSpace(req.Keyword), 0
	if keyword == "" {
		keyword, year = tmdb.GuessTitle(t.name)
	} else {
		keyword, year = tmdb.SplitKeyword(keyword)
	}
	if req.Year > 0 {
		year = req.Year
	}
	if keyword == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "检索关键词为空，请手动填写片名")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 20*time.Second)
	defer cancel()
	list, err := cli.Search(ctx, keyword, year)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	// 前几条候选各补一次详情。两个用处：
	//   1. 候选人选单要显示**主演**（同名电影靠它才分得清是哪一部），而 /search/movie
	//      根本不返回演职员，只有详情接口才有；
	//   2. 第一条的详情顺带当 detail 回给前端 —— 点「刮削」时表单要一次填满（含导演/主演），
	//      apply 失败时也靠它兜底。
	// 多打几次网络请求是必要的代价，所以并发取；取不到只留空，不阻断整次检索。
	movies := fetchTopDetails(ctx, cli, list, scrapeEnrichN)
	for i, m := range movies {
		if m == nil {
			continue
		}
		list[i].Director, list[i].Cast = m.Director, m.Cast
	}
	resp := map[string]any{
		"file": t.name, "keyword": keyword, "year": year,
		"total": len(list), "candidates": list,
	}
	// 故意**不写盘**：检索是"看"，落盘仍由 apply（点候选）或 /fs/edit（保存）负责。
	if len(movies) > 0 && movies[0] != nil {
		m := movies[0]
		resp["detail"] = map[string]any{
			"tmdb_id": m.ID, "title": m.Title, "summary": m.Overview,
			"year": m.Year, "director": m.Director, "cast": m.Cast,
			"region": m.Region, "studio": m.Studio, "language": m.Language,
		}
	}
	return helpers.OK(c, resp)
}

// scrapeEnrichN 给前几条候选补详情。前端也是一行显示三个封面缩略图，对齐这个数。
const scrapeEnrichN = 3

// fetchTopDetails 并发取前 n 条候选的详情，取不到的留 nil（不阻断整次检索）。
// 每条 goroutine 只写自己的下标，无需加锁；Client 建成后不再改字段，可并发用。
func fetchTopDetails(ctx context.Context, cli *tmdb.Client, list []tmdb.Candidate, n int) []*tmdb.Movie {
	if n > len(list) {
		n = len(list)
	}
	out := make([]*tmdb.Movie, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if m, err := cli.Movie(ctx, list[i].ID); err == nil {
				out[i] = m
			}
		}(i)
	}
	wg.Wait()
	return out
}

// ScrapeApply 应用一条 TMDB 结果：写附加信息（简介/导演/年份/地区/出品方/语言/主演）
// 与封面（海报裁成 400×300），并顺带重扫所在目录让 is_new 立刻翻转。
func ScrapeApply(c *echo.Context) error {
	var req ScrapeApplyReq
	if err := c.Bind(&req); err != nil || req.Path == "" || req.TmdbID <= 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	scoped, err := scopePath(c, req.Path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, err.Error())
	}
	t, code, msg := openScrapeTarget(scoped)
	if t == nil {
		return helpers.Fail(c, code, msg)
	}
	defer func() { _ = t.drv.Close() }()

	// 先把这层目录的索引刷新一次，再据此定 sha1 —— 顺序很关键。
	//
	// 海报/附加信息是按 sha1 写进 .mocca 的，而读回（/meta/poster、/fs/info）**只认
	// 索引里的 sha1**。若索引里那条 sha1 已经过期（文件在扫描之后被动过：刚下完、
	// 刚改名、刚替换），下面结尾那次 ScanDir 会把索引重算成另一个值，于是海报落在
	// 旧 sha1 下、读却按新 sha1 去找 —— 现象就是"第一次刮削封面怎么都不出来，第二次才正常"。
	// 先扫一次，让写入与读取用同一个值。
	_ = mediaindex.ScanDir(t.drv, path.Dir(t.rel))
	if sha, _, _ := mediaExtraInfo(t.drv, t.rel); mediaindex.IsSHA1(sha) {
		t.sha1 = sha
	} else if sha, herr := sha1OfRel(t.drv, t.rel); herr == nil && sha != "" {
		// 扫描失败时的兜底：整读算出的就是内容真实 sha1，与 ScanDir 的结果一致。
		t.sha1 = sha
	}

	cli, err := tmdbClient()
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer cancel()
	m, err := cli.Movie(ctx, req.TmdbID)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}

	// 封面只补空缺：已经有封面就不动它（和字段同样的「不覆盖已有内容」口径）。
	// 想换封面走「上传封面」或「FFmpeg 截图」——那两条路本来就是显式替换。
	_, _, hasPoster := mediaExtraInfo(t.drv, t.rel)
	// 海报失败不算整单失败：文字信息已经拿到了，封面可以改用上传/FFmpeg 截图补，
	// 没道理因为一张图把已刮到的简介一起丢掉。把原因回给前端提示即可。
	posterOK, posterErr := hasPoster, ""
	if !hasPoster {
		if raw, ferr := cli.FetchImage(ctx, m.PosterPath); ferr != nil {
			posterErr = ferr.Error()
		} else if png, perr := mediaindex.ResizeCoverPNG(raw); perr != nil {
			posterErr = perr.Error()
		} else if werr := mediaindex.WritePoster(t.drv, t.sha1, png); werr != nil {
			posterErr = werr.Error()
		} else {
			posterOK = true
		}
	}

	old, _ := readSummaryJSON(t.drv, t.sha1)
	sd := mergeSummary(old, m, req.Keep)
	if werr := writeSummaryJSON(t.drv, t.sha1, sd); werr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "保存附加信息失败: "+werr.Error())
	}
	// is_new 的翻转条件是「海报 + 附加信息都在」，这两个刚写下去，
	// 重扫本目录就能立刻翻成 0（列表的 is_new 标记不用等下次全量扫描）。
	// 失败不阻断本次结果：下次扫描仍会修正它。
	_ = mediaindex.ScanDir(t.drv, path.Dir(t.rel))

	return helpers.OK(c, map[string]any{
		"file": t.name, "sha1": t.sha1, "tmdb_id": m.ID, "title": m.Title,
		// 简介也回给前端：它就是刚写进 .mocca 的那份摘要，是"把结果填回表单"的权威来源。
		// 之前漏了它，前端只能退回候选列表里那条短说明，候选没有说明时简介就是空的。
		"summary": sd.Summary,
		"year":    sd.Year, "director": sd.Director, "cast": sd.Cast,
		"region": sd.Region, "studio": sd.Studio, "language": sd.Language,
		"poster": posterOK, "poster_error": posterErr,
		// poster_kept：封面本来就有、这次没动它。前端据此提示"已保留原封面"，
		// 也据此决定要不要刷新封面缓存（没换就不用刷）。
		"poster_kept": hasPoster,
		// 演职员条数：排障用。刮完发现导演/主演是空时，看这个数就知道是
		//「上游没给演职员」（0）还是「给了但没写进去」（>0）。
		"credits_cast": len(m.Cast),
	})
}

// mergeSummary 把 TMDB 详情并进现有附加信息：**只填空缺，不覆盖已有内容**。
//
// 「是否已有内容」以 keep（表单当前状态）为准；keep 为 nil 时退回看存储内容。
// 刮削是「补齐」而不是「重写」：人工填过的、或上一次刮削已经填好的字段一律保留。
// 想换成另一条 TMDB 记录：先把表单里对应字段清空，然后再刮一次。
//
// 地区/出品方/语言不在表单里（界面上不展示），无法表达"用户清空过"，
// 所以这三个一律按存储内容判断。
func mergeSummary(old *summaryData, m *tmdb.Movie, keep *ScrapeKeep) summaryData {
	var sd summaryData
	if old != nil {
		sd = *old
	}
	k := ScrapeKeep{}
	if keep != nil {
		k = *keep
	}
	// hold 报告"这个字段保持原样、不覆盖"：
	//   给了 keep → 以**表单**为准（表单里有内容就别动，哪怕磁盘上是空的）；
	//   没给 → 按存储内容判断（有内容不覆盖）。
	hold := func(formHas, storedHas bool) bool {
		if keep != nil {
			return formHas
		}
		return storedHas
	}
	if !hold(k.Summary, sd.Summary != "") {
		sd.Summary = strings.TrimSpace(m.Overview)
	}
	if !hold(k.Director, sd.Director != "") {
		sd.Director = strings.TrimSpace(m.Director)
	}
	if sd.Region == "" {
		sd.Region = strings.TrimSpace(m.Region)
	}
	if sd.Studio == "" {
		sd.Studio = strings.TrimSpace(m.Studio)
	}
	if sd.Language == "" {
		sd.Language = strings.TrimSpace(m.Language)
	}
	if !hold(k.Year, sd.Year > 0) && m.Year > 0 {
		sd.Year = m.Year
	}
	if !hold(k.Cast, len(sd.Cast) > 0) && len(m.Cast) > 0 {
		sd.Cast = m.Cast
	}
	return sd
}
