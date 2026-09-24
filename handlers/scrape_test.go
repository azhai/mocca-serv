package handlers_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // 只为注册解码器：下面要用 image.Decode 验落盘封面确实是 PNG
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
	"github.com/azhai/mocca/tmdb"
)

// seedVideo 在 root 下写一个视频，并生成一条**与真实 Stat 一致**的索引行（is_new=1）。
//
// 一致性是必须的：扫描的增量判断是 `modified(RFC3339Nano) + size_kb` 相同才复用旧 sha1，
// 随手编一个时间戳会让重扫认定「内容变了」、重新算出另一个 sha1，写在旧 sha1 下的
// 海报/附加信息就再也找不到，is_new 永远翻不过来。
func seedVideo(t *testing.T, root, name, content string) string {
	t.Helper()
	writeTestFile(t, root, name, content)
	st, err := os.Stat(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(content))
	hash := hex.EncodeToString(sum[:])
	line := fmt.Sprintf(`{"name":%q,"size_kb":%d,"modified":%q,"sha1":%q,"is_new":1}`,
		name, (st.Size()+1023)/1024, st.ModTime().UTC().Format(time.RFC3339Nano), hash)
	writeTestFile(t, root, mediaindex.IndexFileName, line+"\n")
	return hash
}

// fakePosterJPEG 假 TMDB 返回的海报：竖版 200×300，尺寸与封面目标(400×300)不同，
// 落盘后必须被裁成 400×300——这样断言尺寸才有意义。
//
// 刻意用 **JPEG** 编码：TMDB 的海报本来就是 JPEG，而且这样「落盘必须仍是 PNG」
// 那条断言才真的有约束力 —— 若哪天改成把原图直接落盘，这里会立刻红。
func fakePosterJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeTMDBAPI 假 TMDB：检索、详情、海报三条路径都覆盖，并把它指给 tmdb 包。
// 返回服务关闭前调用过的检索次数（用来断言「带年份查空后去掉年份重查」）。
func fakeTMDBAPI(t *testing.T) *int {
	t.Helper()
	searchCalls := 0
	poster := fakePosterJPEG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			searchCalls++
			// 带年份时返回空，逼客户端去掉年份重查（模拟从文件名猜错的年份）
			if r.URL.Query().Get("year") != "" {
				w.Write([]byte(`{"results":[]}`))
				return
			}
			w.Write([]byte(`{"results":[{"id":157336,"title":"星际穿越","original_title":"Interstellar",
				"overview":"地球濒临毁灭，一群探险者穿越虫洞。","poster_path":"/abc.jpg",
				"release_date":"2014-11-05","vote_average":8.4}]}`))
		case "/movie/157336":
			w.Write([]byte(`{"id":157336,"title":"星际穿越","original_title":"Interstellar",
				"overview":"地球濒临毁灭，一群探险者穿越虫洞。","poster_path":"/abc.jpg",
				"release_date":"2014-11-05","runtime":169,
				"genres":[{"name":"冒险"}],"production_companies":[{"name":"Legendary Pictures"}],
				"production_countries":[{"name":"美国"}],"spoken_languages":[{"name":"英语"}],
				"credits":{"cast":[{"name":"马修·麦康纳"},{"name":"安妮·海瑟薇"}],
				           "crew":[{"name":"克里斯托弗·诺兰","job":"Director"}]}}`))
		default:
			w.Header().Set("Content-Type", "image/jpeg") // TMDB 海报就是 JPEG
			w.Write(poster)
		}
	}))
	t.Cleanup(srv.Close)

	oldBase, oldImg := tmdb.BaseURL, tmdb.ImageBase
	tmdb.BaseURL, tmdb.ImageBase = srv.URL, srv.URL+"/img"
	t.Cleanup(func() { tmdb.BaseURL, tmdb.ImageBase = oldBase, oldImg })
	return &searchCalls
}

// tmdbMetaPath 设备侧附加信息/海报路径（默认 meta_dir 是内容根下的 .mocca）。
func tmdbMetaPath(t *testing.T, root, sha1hex string, poster bool) string {
	t.Helper()
	var rel string
	var err error
	if poster {
		rel, err = mediaindex.PosterRel(sha1hex)
	} else {
		rel, err = mediaindex.SummaryRel(sha1hex)
	}
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, ".mocca", filepath.FromSlash(rel))
}

func TestScrapeSearchGuessesKeywordAndYear(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	seedVideo(t, root, "Interstellar.2014.1080p.BluRay.mp4", "video-content")
	searchCalls := fakeTMDBAPI(t)
	config.Cfg.TmdbAPIKey = "v3key"
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape", `{"path":"/media/Interstellar.2014.1080p.BluRay.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("检索应成功，got %d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if d["keyword"] != "Interstellar" || d["year"] != float64(2014) {
		t.Errorf("关键词/年份猜错: keyword=%v year=%v", d["keyword"], d["year"])
	}
	if *searchCalls != 2 {
		t.Errorf("带年份查空后应去掉年份重查，检索次数 = %d，want 2", *searchCalls)
	}
	cands, _ := d["candidates"].([]any)
	if len(cands) != 1 {
		t.Fatalf("应有 1 条候选，got %d", len(cands))
	}
	first, _ := cands[0].(map[string]any)
	if first["title"] != "星际穿越" || first["id"] != float64(157336) {
		t.Errorf("候选字段不符: %v", first)
	}
	if p, _ := first["poster"].(string); !strings.HasSuffix(p, "/w500/abc.jpg") {
		t.Errorf("候选海报地址 = %q", p)
	}
	// 候选必须带导演/主演：同名电影的选择卡片上只有封面和主演，而 /search/movie 不返回
	// 演职员 —— 少了这两个字段，前端就只能靠片名判断，同名时必然选错。
	if first["director"] != "克里斯托弗·诺兰" {
		t.Errorf("候选应带导演（详情接口才有）: %v", first["director"])
	}
	if cast, _ := first["cast"].([]any); len(cast) != 2 {
		t.Errorf("候选应带主演（详情接口才有）: %v", first["cast"])
	}

	// 检索要顺带带回**第一条候选的详情**：候选里没有导演/主演，没有这份 detail
	// 前端点「刮削」后表单只能填一半（导演、主演是空的）。
	det, _ := d["detail"].(map[string]any)
	if det == nil {
		t.Fatal("检索响应缺少 detail：表单将填不上导演/主演")
	}
	if det["director"] != "克里斯托弗·诺兰" || det["year"] != float64(2014) {
		t.Errorf("detail 导演/年份不符: %v", det)
	}
	if cast, _ := det["cast"].([]any); len(cast) != 2 {
		t.Errorf("detail 主演应有 2 位，got %v", det["cast"])
	}
	if s, _ := det["summary"].(string); !strings.Contains(s, "虫洞") {
		t.Errorf("detail 简介不符: %v", det["summary"])
	}
	// 只是"看"，不能顺手落盘：这一步不该写出 .mocca。
	if _, err := os.Stat(filepath.Join(root, ".mocca")); err == nil {
		t.Error("检索不该写 .mocca：detail 是只读预览，落盘由 apply / fs/edit 负责")
	}
}

// TestScrapeUsesConfiguredProxy 端到端：TMDB_PROXY 必须真的接进刮削调用链。
// 目标域名故意用不可解析的 .invalid —— 只有走了代理才会成功，直连必然 DNS 失败，
// 所以这比「看代理有没有被访问过」更硬。
func TestScrapeUsesConfiguredProxy(t *testing.T) {
	var proxied int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied++
		// 走代理时，代理看到的是带 /3 前缀的绝对地址
		if strings.HasSuffix(r.URL.Path, "/search/movie") {
			w.Write([]byte(`{"results":[{"id":1,"title":"星际穿越","original_title":"Interstellar",
				"release_date":"2014-11-05","vote_average":8.4}]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer proxy.Close()

	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	seedVideo(t, root, "Interstellar.2014.mp4", "video-content")

	oldBase := tmdb.BaseURL
	tmdb.BaseURL = "http://tmdb.invalid/3" // 不可解析：不经代理就一定失败
	defer func() { tmdb.BaseURL = oldBase }()

	config.Cfg.TmdbAPIKey = "v3key"
	config.Cfg.TmdbProxy = proxy.URL
	defer func() { config.Cfg.TmdbProxy = "" }()

	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape",
		`{"path":"/media/Interstellar.2014.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("配了代理后刮削应成功，got %d msg=%v", code, resp["message"])
	}
	if proxied == 0 {
		t.Error("请求没有走代理")
	}

	// 代理地址写错时，要给一句可操作的错，而不是一句难懂的 URL 解析错
	config.Cfg.TmdbProxy = "://bad"
	code, resp = call(t, e, http.MethodPost, "/api/fs/scrape",
		`{"path":"/media/Interstellar.2014.mp4"}`, admin)
	if code == 200 {
		t.Error("非法代理地址应当报错")
	} else if msg, _ := resp["message"].(string); !strings.Contains(msg, "代理地址无效") {
		t.Errorf("错误里应说明代理地址无效，got %q", msg)
	}
}

// TestScrapeApplyWritesDeviceMetaAndPoster 端到端：命中接口 → 落 .mocca 附加信息 + 封面
// → 重扫把 is_new 翻成 0（列表的「待刮削」标记不用等下次全量扫描）。
func TestScrapeApplyWritesDeviceMetaAndPoster(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	hash := seedVideo(t, root, "Interstellar.2014.1080p.BluRay.mp4", "video-content")
	fakeTMDBAPI(t)
	config.Cfg.TmdbAPIKey = "v3key"
	config.Cfg.TmdbLang = "" // 走缺省 zh-CN
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape/apply",
		`{"path":"/media/Interstellar.2014.1080p.BluRay.mp4","tmdb_id":157336}`, admin)
	if code != 200 {
		t.Fatalf("应用应成功，got %d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if d["title"] != "星际穿越" || d["poster"] != true || d["poster_error"] != "" {
		t.Errorf("响应不符: %v", d)
	}
	if d["director"] != "克里斯托弗·诺兰" || d["year"] != float64(2014) {
		t.Errorf("导演/年份不符: %v %v", d["director"], d["year"])
	}

	// 1) 附加信息写进 .mocca/<sha1>.meta，不是 sqlite
	raw, err := os.ReadFile(tmdbMetaPath(t, root, hash, false))
	if err != nil {
		t.Fatalf("读附加信息失败: %v", err)
	}
	var sd struct {
		Summary  string   `json:"summary"`
		Director string   `json:"director"`
		Year     int      `json:"year"`
		Region   string   `json:"region"`
		Studio   string   `json:"studio"`
		Language string   `json:"language"`
		Cast     []string `json:"cast"`
	}
	if err := json.Unmarshal(raw, &sd); err != nil {
		t.Fatalf("附加信息不是合法 JSON: %v, body=%s", err, raw)
	}
	if sd.Director != "克里斯托弗·诺兰" || sd.Year != 2014 || sd.Region != "美国" ||
		sd.Studio != "Legendary Pictures" || sd.Language != "英语" {
		t.Errorf("附加信息不符: %+v", sd)
	}
	if !strings.Contains(sd.Summary, "虫洞") || len(sd.Cast) != 2 {
		t.Errorf("简介/主演不符: %+v", sd)
	}
	// 回包里的简介必须与刚落盘的一致：前端拿它直接填表单，缺了就只能退回候选列表
	// 那条短说明（候选没有说明时简介会是空的），所以这里钉住"回包 == 落盘"。
	if d["summary"] != sd.Summary {
		t.Errorf("回包简介与落盘不一致: 回包=%v 落盘=%v", d["summary"], sd.Summary)
	}

	// 2) 海报按 FsCov/FsShot 的统一口径落盘：400×300 PNG
	pf, err := os.Open(tmdbMetaPath(t, root, hash, true))
	if err != nil {
		t.Fatalf("读封面失败: %v", err)
	}
	defer func() { _ = pf.Close() }()
	head := make([]byte, 8)
	if _, err := io.ReadFull(pf, head); err != nil {
		t.Fatalf("读封面头失败: %v", err)
	}
	// 目录名与扩展名是 .png，内容也必须是 PNG —— TMDB 给的多半是 JPEG，
	// 全靠 ResizeCoverPNG 统一转码；这里钉住，防止哪天改成直接落原图。
	if !bytes.Equal(head, []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("封面不是 PNG（头部 = %q），应经 ResizeCoverPNG 统一转码", head)
	}
	if _, err := pf.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(pf)
	if err != nil {
		t.Fatalf("封面不是可解码图片: %v", err)
	}
	if format != "png" {
		t.Errorf("封面格式 = %q，want png", format)
	}
	if b := img.Bounds(); b.Dx() != mediaindex.CoverWidth || b.Dy() != mediaindex.CoverHeight {
		t.Errorf("封面尺寸 = %dx%d，want %dx%d", b.Dx(), b.Dy(), mediaindex.CoverWidth, mediaindex.CoverHeight)
	}

	// 3) is_new 翻转：海报 + 附加信息都在了
	idx, err := os.ReadFile(filepath.Join(root, mediaindex.IndexFileName))
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		SHA1  string `json:"sha1"`
		IsNew int    `json:"is_new"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(idx), &rec); err != nil {
		t.Fatalf("索引行不是合法 JSON: %v, line=%s", err, idx)
	}
	if rec.IsNew != 0 || rec.SHA1 != hash {
		t.Errorf("刮削后索引应 is_new=0 且 sha1 不变，got %+v", rec)
	}
}

// TestScrapeKeepsManualFieldsAndDegradesOnPosterFailure 两条降级行为一起钉住：
//   - 已有内容的字段**一律不覆盖**（刮削只补空缺），TMDB 没给的更不清空；
//   - 海报下载失败只提示，文字信息照常落盘。
func TestScrapeKeepsManualFieldsAndDegradesOnPosterFailure(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	hash := seedVideo(t, root, "Interstellar.2014.mkv", "video-content")
	config.Cfg.TmdbAPIKey = "v3key"
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 先人工填两条：简介（TMDB 也有值，必须**保留**不覆盖）、区域（TMDB 没有，更不该动）
	sr, err := mediaindex.SummaryRel(hash)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, filepath.Join(".mocca", filepath.ToSlash(sr)),
		`{"summary":"手填简介","region":"日本","lead":["原创歌手"]}`)

	// 假 TMDB：详情能给，但海报路径为空（FetchImage 会直接报「该条目没有海报」）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			w.Write([]byte(`{"results":[]}`))
		case "/movie/9":
			w.Write([]byte(`{"id":9,"title":"星际穿越","release_date":"2014-11-05","overview":"简介",
				"poster_path":"","credits":{"cast":[],"crew":[]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	oldBase, oldImg := tmdb.BaseURL, tmdb.ImageBase
	tmdb.BaseURL, tmdb.ImageBase = srv.URL, srv.URL+"/img"
	defer func() { tmdb.BaseURL, tmdb.ImageBase = oldBase, oldImg }()

	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape/apply",
		`{"path":"/media/Interstellar.2014.mkv","tmdb_id":9}`, admin)
	if code != 200 {
		t.Fatalf("海报缺失时也应成功，got %d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if d["poster"] != false || d["poster_error"] == "" {
		t.Errorf("应回海报失败与原因: poster=%v err=%v", d["poster"], d["poster_error"])
	}

	raw, err := os.ReadFile(tmdbMetaPath(t, root, hash, false))
	if err != nil {
		t.Fatalf("附加信息应照常落盘: %v", err)
	}
	var sd map[string]any
	if err := json.Unmarshal(raw, &sd); err != nil {
		t.Fatal(err)
	}
	if sd["summary"] != "手填简介" {
		t.Errorf("已有内容的字段不该被覆盖: summary=%v", sd["summary"])
	}
	if sd["year"] != float64(2014) {
		t.Errorf("空着的字段应被补上: year=%v", sd["year"])
	}
	if sd["region"] != "日本" {
		t.Errorf("TMDB 没给该字段时不该清空人工填的内容，region=%v", sd["region"])
	}
	if leads, _ := sd["lead"].([]any); len(leads) != 1 {
		t.Errorf("无关字段不该被动过: %v", sd["lead"])
	}
}

// TestScrapeApplyPosterReadableOnFreshScrape 刮削后封面必须**立刻**能读回来。
//
// 这条守着一个只在真实环境才暴露的坑：海报按 sha1 写进 .mocca，而读回（/meta/poster）
// **只认 .index.jsonl 里的 sha1**。若索引那条 sha1 已过期（文件在扫描之后被动过：刚下完、
// 刚改名、刚替换），apply 结尾的 ScanDir 会把索引重算成另一个值，海报就落在旧 sha1 下、
// 读却按新 sha1 去找 —— 表现为"第一次刮削封面怎么都不出来，第二次才正常"。
// 修法：先刷新索引、再用刷新后的 sha1 写（见 ScrapeApply 开头那段 ScanDir）。
func TestScrapeApplyPosterReadableOnFreshScrape(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)

	// 视频 + 一条**过期**的索引行：sha1 与 size_kb/modified 都对不上真实文件。
	// 用 .mkv 而不是 .mp4：这是真实片源最常见的容器，也顺带守住"索引收录的扩展名
	// 必须覆盖浏览列表能识别的那些"（.mkv 曾经进不了索引，海报因此一直 404）。
	writeTestFile(t, root, "Stale.2019.mkv", "video-content")
	writeTestFile(t, root, ".index.jsonl",
		`{"name":"Stale.2019.mkv","size_kb":1,"modified":"2020-01-01T00:00:00Z",`+
			`"sha1":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","is_new":1}`+"\n")
	fakeTMDBAPI(t)
	config.Cfg.TmdbAPIKey = "v3key"
	config.Cfg.TmdbLang = ""
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape/apply",
		`{"path":"/media/Stale.2019.mkv","tmdb_id":157336}`, admin)
	if code != 200 {
		t.Fatalf("刮削应成功，got %d msg=%v", code, resp["message"])
	}
	if d, _ := resp["data"].(map[string]any); d["poster"] != true {
		t.Fatalf("海报应落盘成功: %v", d)
	}

	// 关键：走 HTTP 取封面。第一次就必须可用，不能等再刮一次。
	req := httptest.NewRequest(http.MethodGet, "/meta/poster?path=%2Fmedia%2FStale.2019.mkv", nil)
	req.Header.Set("Authorization", admin)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("刮削后 /meta/poster 应立刻可用，got %d：海报写在了旧 sha1 下，读仍按新 sha1 找", rec.Code)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Error("取回的封面不是 PNG")
	}
}

// TestScrapeKeepFollowsFormNotStorage 覆盖与否以**表单**为准，而不是 .mocca 里存了什么。
//
// 场景：磁盘上四个字段都有旧值，而用户已经在表单里把「导演」清空了（keep.director=false）。
// 这时「导演」应当被 TMDB 的值补上，其余三个保持旧值；地区不在表单里（界面不展示），
// 永远只补空缺。
func TestScrapeKeepFollowsFormNotStorage(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	hash := seedVideo(t, root, "Interstellar.2014.1080p.BluRay.mp4", "video-content")
	fakeTMDBAPI(t)
	config.Cfg.TmdbAPIKey = "v3key"
	config.Cfg.TmdbLang = ""
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 磁盘上四个字段都有旧值
	sr, err := mediaindex.SummaryRel(hash)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, filepath.Join(".mocca", filepath.ToSlash(sr)),
		`{"summary":"旧简介","director":"旧导演","year":1999,"cast":["旧主演"],"region":"日本"}`)

	// 表单里只剩简介与主演有内容；导演、年份已被用户清空
	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape/apply",
		`{"path":"/media/Interstellar.2014.1080p.BluRay.mp4","tmdb_id":157336,`+
			`"keep":{"summary":true,"director":false,"cast":true,"year":false}}`, admin)
	if code != 200 {
		t.Fatalf("刮削应成功，got %d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if d["summary"] != "旧简介" {
		t.Errorf("表单里还有内容，不该被覆盖: %v", d["summary"])
	}
	if d["director"] != "克里斯托弗·诺兰" {
		t.Errorf("表单里已清空，应用 TMDB 的值补上: %v", d["director"])
	}
	if d["year"] != float64(2014) {
		t.Errorf("表单里已清空，年份应补上: %v", d["year"])
	}
	if cast, _ := d["cast"].([]any); len(cast) != 1 {
		t.Errorf("表单里还有内容，主演不该被覆盖: %v", d["cast"])
	}
	if d["region"] != "日本" {
		t.Errorf("不在表单里的字段只补空缺: %v", d["region"])
	}

	// 落盘结果与回包一致
	raw, err := os.ReadFile(tmdbMetaPath(t, root, hash, false))
	if err != nil {
		t.Fatalf("读附加信息失败: %v", err)
	}
	var sd map[string]any
	if err := json.Unmarshal(raw, &sd); err != nil {
		t.Fatal(err)
	}
	if sd["director"] != "克里斯托弗·诺兰" || sd["summary"] != "旧简介" {
		t.Errorf("落盘与预期不符: %v", sd)
	}
}

// TestScrapeKeepsExistingPoster 封面同样"只补空缺"：已经有封面就不覆盖。
// 换封面走「上传封面」/「FFmpeg 截图」，那是显式替换；刮削不该偷偷把用户认可的封面换掉。
func TestScrapeKeepsExistingPoster(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	hash := seedVideo(t, root, "Interstellar.2014.mkv", "video-content")
	fakeTMDBAPI(t) // 这套假 TMDB 是**能给**海报的，正好用来验证"能拿也不覆盖"
	config.Cfg.TmdbAPIKey = "v3key"
	config.Cfg.TmdbLang = ""
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 先把"用户已经认可的封面"放到 .mocca 里（内容随便，能读出来即可）
	sr, err := mediaindex.PosterRel(hash)
	if err != nil {
		t.Fatal(err)
	}
	posterPath := filepath.Join(root, ".mocca", filepath.ToSlash(sr))
	writeTestFile(t, root, filepath.Join(".mocca", filepath.ToSlash(sr)), "USER-POSTER")

	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape/apply",
		`{"path":"/media/Interstellar.2014.mkv","tmdb_id":157336}`, admin)
	if code != 200 {
		t.Fatalf("刮削应成功，got %d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if d["poster_kept"] != true {
		t.Errorf("应告知封面被保留: %v", d)
	}

	// 封面文件必须原样不动
	got, err := os.ReadFile(posterPath)
	if err != nil {
		t.Fatalf("读封面失败: %v", err)
	}
	if string(got) != "USER-POSTER" {
		t.Error("已有封面被刮削覆盖了，应保持原样")
	}

	// 但空着的字段照样要补上（封面保留 ≠ 什么都不做）
	raw, err := os.ReadFile(tmdbMetaPath(t, root, hash, false))
	if err != nil {
		t.Fatalf("读附加信息失败: %v", err)
	}
	var sd map[string]any
	if err := json.Unmarshal(raw, &sd); err != nil {
		t.Fatal(err)
	}
	if sd["director"] != "克里斯托弗·诺兰" {
		t.Errorf("空着的字段应被补上: %v", sd)
	}
}

// TestFsGetExposesCover 封面必须在 /fs/get 的 cover 字段里给出。
// 之前这个字段**从来没被赋值过**（mediaExtraInfo 的第三个返回值被丢掉了），
// 调用方拿到的永远是空 —— 而海报本身是统一处理过的 400×300 PNG。
func TestFsGetExposesCover(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	hash := seedVideo(t, root, "Interstellar.2014.mp4", "video-content")
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	const p = "/media/Interstellar.2014.mp4"

	// 还没有海报 → cover 应为空
	code, resp := call(t, e, http.MethodPost, "/api/fs/get", `{"path":"`+p+`"}`, admin)
	if code != 200 {
		t.Fatalf("取详情失败: code=%d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if v, _ := d["cover"].(string); v != "" {
		t.Errorf("没有海报时 cover 应为空，got %q", v)
	}

	// 放一张海报到约定位置（.mocca/xx/xx/<sha1>.png）
	poster := tmdbMetaPath(t, root, hash, true)
	if err := os.MkdirAll(filepath.Dir(poster), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poster, fakePosterJPEG(t), 0o600); err != nil {
		t.Fatal(err)
	}

	_, resp = call(t, e, http.MethodPost, "/api/fs/get", `{"path":"`+p+`"}`, admin)
	d, _ = resp["data"].(map[string]any)
	got, _ := d["cover"].(string)
	want, err := mediaindex.PosterRel(hash)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.ToSlash(want) {
		t.Errorf("cover = %q, want %q", got, want)
	}
}

// TestScrapeRequiresAdminAndVideo 权限与类型边界：接口既调外网又写磁盘，
// 只有管理员能碰；音频/图片刮不出东西，直接 400。
func TestScrapeRequiresAdminAndVideo(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	seedVideo(t, root, "a.mp4", "video-content")
	writeTestFile(t, root, "b.mp3", "audio-content")

	// 未登录 401 / 普通用户 403（管理员组由中间件把关）
	if code, _ := call(t, e, http.MethodPost, "/api/fs/scrape", `{"path":"/media/a.mp4"}`, ""); code != 401 {
		t.Errorf("未登录应 401，got %d", code)
	}
	user := makeUser(t, e, "u", "p", models.RoleGeneral)
	if code, _ := call(t, e, http.MethodPost, "/api/fs/scrape", `{"path":"/media/a.mp4"}`, user); code != 403 {
		t.Errorf("普通用户应 403，got %d", code)
	}

	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	// 没配密钥：给一句可操作的原因，而不是空指针或「内部错误」
	config.Cfg.TmdbAPIKey = ""
	code, resp := call(t, e, http.MethodPost, "/api/fs/scrape", `{"path":"/media/a.mp4"}`, admin)
	if code != 400 || !contains(respBody(resp), "TMDB_KEY") {
		t.Errorf("未配置密钥应 400 且说明 TMDB_KEY，got %d %v", code, resp["message"])
	}

	config.Cfg.TmdbAPIKey = "v3key"
	if code, resp = call(t, e, http.MethodPost, "/api/fs/scrape", `{"path":"/media/b.mp3"}`, admin); code != 400 {
		t.Errorf("音频应 400，got %d", code)
	} else if !contains(respBody(resp), "视频") {
		t.Errorf("应说明只支持视频，got %v", resp["message"])
	}
	// 应用接口缺 tmdb_id 直接 400（ID 0 不是合法条目）
	if code, _ := call(t, e, http.MethodPost, "/api/fs/scrape/apply", `{"path":"/media/a.mp4"}`, admin); code != 400 {
		t.Errorf("缺 tmdb_id 应 400，got %d", code)
	}
}
