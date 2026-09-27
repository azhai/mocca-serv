// Package tmdb 调用 TMDB 的公开 REST API：按片名检索电影、取详情与演职员，再拼海报地址。
//
// 只依赖标准库（net/http + encoding/json）：本服务对第三方 SDK 一律不进依赖树，
// TMDB 的接口只有三个端点，写一层薄封装比引一个 SDK 更可控。
//
// 鉴权两种密钥都能用：v3 的 api_key（32 位十六进制）走查询串，
// v4 的 API Read Access Token（JWT，两段点）走 Authorization: Bearer —— 由密钥形态自动判断。
package tmdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// API 与图片服务的基地址。写成包级变量（而非常量）只为两件事：
// 测试里换成 httptest 的假服务；以及线上换域名/指向自建反代时改一行即可。
var (
	// BaseURL 是 TMDB 的 API 基地址。
	//
	// api.themoviedb.org 是文档里的规范域名，但它在本机实测**连接超时**（部分网络下不可达）。
	// 别名 api.tmdb.org 依然返回标准 TMDB 响应 —— 不带密钥时回
	// {"status_code":7,"status_message":"Invalid API key"}，说明它确实是同一个 API。
	// 所以默认用它；要换回规范域名或指向自建反代，改这一行即可。
	BaseURL   = "https://api.tmdb.org/3"
	ImageBase = "https://image.tmdb.org/t/p"
)

// DefaultLang 缺省语言。中文用户要中文简介与译名，没有理由默认英文。
const DefaultLang = "zh-CN"

// PosterSize 海报取 w500：宽 500 已远超封面落盘的 400×300，再大只是浪费带宽。
const PosterSize = "w500"

// MaxCast 主演最多取几位。卡片上一行放不下太多，五位是够用又不挤的口径。
const MaxCast = 5

// MaxDirector 导演最多取几位。联合执导也只留一位，避免一格塞太多。
const MaxDirector = 1

// maxImageBytes 单张海报的下载上限，防着对方返回异常大的文件。
const maxImageBytes = 8 << 20

// ErrNoAPIKey 未配置密钥。调用方据此给用户一句可操作的中文提示。
var ErrNoAPIKey = errors.New("未配置 TMDB_KEY，无法刮削")

// named 只有名字的条目：制片公司 / 出品国家 / 语言 / 类型都是这个形状。
type named struct {
	Name string `json:"name"`
}

// person 演职员：crew 靠 Job/Department 区分职务，cast 靠 Order 排位次。
//
// 字段比"只取名字"多一点是刻意的：只认 job=="Director" 太脆 —— 不同语言下出现过
// 「导演」，也有的条目只填了 department。多留几条等价写法，才能稳稳认出导演。
type person struct {
	Name               string `json:"name"`
	Job                string `json:"job"`
	Department         string `json:"department"`
	KnownForDepartment string `json:"known_for_department"`
	Order              int    `json:"order"`
}

// Candidate 检索结果里的一条，够前端列出来让人挑。
type Candidate struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title,omitempty"`
	Year          int     `json:"year,omitempty"`
	Overview      string  `json:"overview,omitempty"`
	Poster        string  `json:"poster,omitempty"` // 完整海报地址，可直接 <img src>
	VoteAverage   float64 `json:"vote_average,omitempty"`
	// Director / Cast 不是 /search/movie 的字段，是详情接口才有的。
	// 检索接口（handlers.ScrapeSearch）会给前几条候选各补一次详情填上它们 ——
	// 同名电影（翻拍、续集、译名撞车）光看片名和年份分不清是哪一部，得靠主演来认。
	Director string   `json:"director,omitempty"`
	Cast     []string `json:"cast,omitempty"`
}

// Movie 详情：字段按 .mocca 附加信息与前端展示的需要挑选，不做全量透传。
type Movie struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title,omitempty"`
	Year          int      `json:"year,omitempty"`
	Released      string   `json:"released,omitempty"`
	Runtime       int      `json:"runtime,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	Director      string   `json:"director,omitempty"`
	Cast          []string `json:"cast,omitempty"`
	Region        string   `json:"region,omitempty"`
	Studio        string   `json:"studio,omitempty"`
	Language      string   `json:"language,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	VoteAverage   float64  `json:"vote_average,omitempty"`
	// Poster 完整地址供展示；PosterPath 是 TMDB 的相对路径，下载海报时用。
	Poster     string `json:"poster,omitempty"`
	PosterPath string `json:"-"`
}

// Client 一个已配好密钥与语言的调用端。零值不可用，用 New 构造。
type Client struct {
	apiKey string
	lang   string
	base   string
	http   *http.Client
	// proxy 仅用于错误信息里说明「这次走没走代理」，不参与请求构造（那是 http.Transport 的事）。
	proxy string
}

// New 建一个客户端。lang 留空则用 DefaultLang；密钥为空返回 ErrNoAPIKey。
//
// proxy 非空时所有请求（含海报下载）都走该 HTTP 代理，形如 http://127.0.0.1:7890；
// 留空则保持 Transport 为空 —— 即沿用 http.DefaultTransport 的行为，它会读**进程环境**
// 的 HTTPS_PROXY/HTTP_PROXY。代理地址非法时在这里就报错，而不是等用户点了刮削才炸。
func New(apiKey, lang, proxy string) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	if strings.TrimSpace(lang) == "" {
		lang = DefaultLang
	}
	proxy = strings.TrimSpace(proxy)
	// 上游慢不该拖住本服务：单次请求 15 秒封顶
	hc := &http.Client{Timeout: 15 * time.Second}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Host == "" || u.Scheme == "" {
			return nil, errors.Errorf("刮削代理地址无效: %q", proxy)
		}
		// 克隆 DefaultTransport 而不是从零造：保留拨号超时、TLS 配置、HTTP/2 等默认值，
		// 只把 Proxy 换掉（默认值里那句是 ProxyFromEnvironment，会被这个覆盖）。
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = http.ProxyURL(u)
		hc.Transport = tr
	}
	return &Client{
		apiKey: apiKey,
		lang:   lang,
		base:   strings.TrimRight(BaseURL, "/"),
		http:   hc,
		proxy:  proxy,
	}, nil
}

// proxyNote 说明这一请求走没走代理。排障时最需要这句：TLS 握手超时之类的错，
// 光看目标 URL 分不清是「代理没生效」还是「代理出口也不通」。
func (c *Client) proxyNote() string {
	if c.proxy == "" {
		return "未走代理"
	}
	return "代理 " + redactUserInfo(c.proxy)
}

// redactKey 把文本里出现的密钥打码。
//
// 起因是一次真实事故：*url.Error 的 Error() 里嵌着完整请求 URL，而 v3 密钥是以
// ?api_key= 的形式挂在查询串上的 —— 原样透给界面就等于把密钥印在页面上、
// 也顺手写进了日志。所以对外（含错误信息）只留 api_key=***。
func redactKey(s, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return s
	}
	// 编码形态与原样形态都可能出现，两个都替
	for _, form := range []string{url.QueryEscape(key), key} {
		if form != "" {
			s = strings.ReplaceAll(s, form, "***")
		}
	}
	return s
}

// redactUserInfo 去掉代理 URL 里的账号口令（http://user:pass@host:port 这种）。
func redactUserInfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("***")
	return u.String()
}

// Search 按片名（可带年份）检索电影。year<=0 表示不限年份。
//
// TMDB 的 year 是**严格过滤**：猜错年份会直接空结果。所以带年份查不到时
// 自动去掉年份再查一次——宁可多回几条近似结果，也不要给用户一个空列表。
//
// 拿到结果后统一交给 rankCandidates 重排：完全符合片名的排在最前，其次才看年份
// （见 parse.go）。前端展示与默认填充都取排序后的第一条，这一步直接决定刮得准不准。
func (c *Client) Search(ctx context.Context, query string, year int) ([]Candidate, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("检索关键词为空")
	}
	if year > 0 {
		list, err := c.search(ctx, query, year)
		if err != nil {
			return nil, err
		}
		if len(list) > 0 {
			rankCandidates(list, query, year)
			return list, nil
		}
	}
	list, err := c.search(ctx, query, 0)
	if err != nil {
		return nil, err
	}
	rankCandidates(list, query, year)
	return list, nil
}

// search 一次检索；year<=0 则不传年份。
func (c *Client) search(ctx context.Context, query string, year int) ([]Candidate, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	var resp struct {
		Results []struct {
			ID            int     `json:"id"`
			Title         string  `json:"title"`
			OriginalTitle string  `json:"original_title"`
			Overview      string  `json:"overview"`
			PosterPath    string  `json:"poster_path"`
			ReleaseDate   string  `json:"release_date"`
			VoteAverage   float64 `json:"vote_average"`
		} `json:"results"`
	}
	if err := c.getJSON(ctx, "/search/movie", q, &resp); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Candidate{
			ID: r.ID, Title: r.Title, OriginalTitle: r.OriginalTitle,
			Year: yearOf(r.ReleaseDate), Overview: r.Overview,
			Poster: c.PosterURL(r.PosterPath), VoteAverage: r.VoteAverage,
		})
	}
	return out, nil
}

// Movie 取单部电影详情，附带 credits 里的导演与主演。
func (c *Client) Movie(ctx context.Context, id int) (*Movie, error) {
	if id <= 0 {
		return nil, errors.New("TMDB id 无效")
	}
	q := url.Values{}
	q.Set("append_to_response", "credits")
	var resp struct {
		ID                  int     `json:"id"`
		Title               string  `json:"title"`
		OriginalTitle       string  `json:"original_title"`
		Overview            string  `json:"overview"`
		PosterPath          string  `json:"poster_path"`
		ReleaseDate         string  `json:"release_date"`
		Runtime             int     `json:"runtime"`
		VoteAverage         float64 `json:"vote_average"`
		Genres              []named `json:"genres"`
		ProductionCompanies []named `json:"production_companies"`
		ProductionCountries []named `json:"production_countries"`
		SpokenLanguages     []named `json:"spoken_languages"`
		Credits             struct {
			Cast []person `json:"cast"`
			Crew []person `json:"crew"`
		} `json:"credits"`
	}
	if err := c.getJSON(ctx, "/movie/"+strconv.Itoa(id), q, &resp); err != nil {
		return nil, err
	}

	// 演职员兜底：详情里的 append_to_response=credits 在个别环境/镜像上拿不到内容，
	// 表现为「简介年份都有、导演和主演却是空的」。这时改用独立端点 /movie/<id>/credits
	// 再取一次（golang-tmdb 也是把 credits 当独立端点用）。取不到也不算失败 ——
	// 文字信息照常落盘，只是没有演职员。
	cast, crew := resp.Credits.Cast, resp.Credits.Crew
	if len(cast) == 0 && len(crew) == 0 {
		if c2, cr2, err := c.movieCredits(ctx, id); err == nil {
			cast, crew = c2, cr2
		}
	}

	m := &Movie{
		ID: resp.ID, Title: resp.Title, OriginalTitle: resp.OriginalTitle,
		Year: yearOf(resp.ReleaseDate), Released: resp.ReleaseDate,
		Runtime: resp.Runtime, Overview: resp.Overview,
		VoteAverage: resp.VoteAverage,
		PosterPath:  resp.PosterPath,
		Poster:      c.PosterURL(resp.PosterPath),
		// 导演只留一位；出品方/地区/语言各留少量，避免一格塞满
		Director: joinNames(directorNames(crew), "、", MaxDirector),
		Cast:     castNames(cast),
		Region:   joinNames(structNames(resp.ProductionCountries), "、", 3),
		Studio:   joinNames(structNames(resp.ProductionCompanies), "、", 3),
		Language: joinNames(structNames(resp.SpokenLanguages), "、", 2),
		Genres:   uniqueNames(structNames(resp.Genres)),
	}
	if m.Title == "" {
		m.Title = m.OriginalTitle
	}
	return m, nil
}

// movieCredits 从独立端点取演职员：`/movie/<id>/credits`。
// 只在详情的 append_to_response 没带回来时兜底用。
func (c *Client) movieCredits(ctx context.Context, id int) (cast, crew []person, err error) {
	var resp struct {
		Cast []person `json:"cast"`
		Crew []person `json:"crew"`
	}
	if err := c.getJSON(ctx, "/movie/"+strconv.Itoa(id)+"/credits", nil, &resp); err != nil {
		return nil, nil, err
	}
	return resp.Cast, resp.Crew, nil
}

// PosterURL 把 TMDB 的相对海报路径拼成完整地址；路径为空返回空串。
func (c *Client) PosterURL(posterPath string, size ...string) string {
	posterPath = strings.TrimSpace(posterPath)
	if posterPath == "" {
		return ""
	}
	sz := PosterSize
	if len(size) > 0 && size[0] != "" {
		sz = size[0]
	}
	return strings.TrimRight(ImageBase, "/") + "/" + sz + "/" + strings.TrimPrefix(posterPath, "/")
}

// FetchImage 下载海报原图字节。用于落盘成 .mocca 封面（再由调用方统一裁剪编码）。
func (c *Client) FetchImage(ctx context.Context, posterPath string) ([]byte, error) {
	imgURL := c.PosterURL(posterPath)
	if imgURL == "" {
		return nil, errors.New("该条目没有海报")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Errorf("下载海报失败（%s）: %s", c.proxyNote(), redactKey(err.Error(), c.apiKey))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("下载海报失败：HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, errors.Wrap(err, "读取海报失败")
	}
	if len(data) == 0 {
		return nil, errors.New("海报内容为空")
	}
	return data, nil
}

// getJSON 发一次 GET 并解出 JSON。密钥按形态决定放查询串还是请求头。
func (c *Client) getJSON(ctx context.Context, apiPath string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("language") == "" {
		q.Set("language", c.lang)
	}
	// v4 的读令牌是 JWT（形如 xxx.yyy.zzz），只认 Authorization；v3 的密钥相反。
	useBearer := strings.Count(c.apiKey, ".") == 2
	if !useBearer {
		q.Set("api_key", c.apiKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+apiPath+"?"+q.Encode(), nil)
	if err != nil {
		return errors.WithStack(err)
	}
	if useBearer {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// 不能 Wrap 了事：原始错误里带着含 api_key 的完整 URL，必须打码后再外发
		return errors.Errorf("调用 TMDB 失败（%s）: %s", c.proxyNote(), redactKey(err.Error(), c.apiKey))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return errors.Wrap(err, "读取 TMDB 响应失败")
	}
	if resp.StatusCode != http.StatusOK {
		// TMDB 的错误体里有 status_message，能拿到就直接透给用户（如「密钥无效」）
		var e struct {
			StatusMessage string `json:"status_message"`
		}
		_ = json.Unmarshal(body, &e)
		msg := strings.TrimSpace(e.StatusMessage)
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		if msg == "" {
			msg = resp.Status
		}
		return errors.Errorf("TMDB 返回 %d：%s", resp.StatusCode, msg)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.Wrap(err, "解析 TMDB 响应失败")
	}
	return nil
}

// yearOf 从 `2014-11-05` 这类发布日期里取年份，取不到返回 0。
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil || y < 1800 || y > 2200 {
		return 0
	}
	return y
}

// directorNames 从 crew 里认出导演，去重并保持 TMDB 给出的顺序。
//
// 两级：先按**职务**精确匹配（Director / 导演 / Co-Director 这类含 Director 的写法）；
// 一条都没匹配到时，才退到按**部门**匹配（Directing）。这样"助理导演"这类
// 只出现在导演组、但职务不是 Director 的人不会被误当成导演。
func directorNames(crew []person) []string {
	seen := map[string]bool{}
	var out []string

	// 第一级：职务明确写着导演。刻意**只做精确匹配、不做子串匹配** ——
	// 否则 "Assistant Director"（助理导演）会被算成导演。
	for _, cr := range crew {
		if !isDirectorJob(cr.Job) || cr.Name == "" || seen[cr.Name] {
			continue
		}
		seen[cr.Name] = true
		out = append(out, cr.Name)
	}
	if len(out) > 0 {
		return out
	}

	// 第二级：这条**完全没有职务信息**，但人在导演组，才按导演算。
	// 有职务却不是导演的（如 Assistant Director）一律不认。
	for _, cr := range crew {
		if strings.TrimSpace(cr.Job) != "" || !isDirectingDept(cr) || cr.Name == "" || seen[cr.Name] {
			continue
		}
		seen[cr.Name] = true
		out = append(out, cr.Name)
	}
	return out
}

// isDirectorJob 职务是不是导演（中英文写法都认）。
func isDirectorJob(job string) bool {
	switch strings.ToLower(strings.TrimSpace(job)) {
	case "director", "co-director", "导演", "联合导演":
		return true
	}
	return false
}

// isDirectingDept 部门是不是导演组。
func isDirectingDept(p person) bool {
	for _, d := range []string{p.Department, p.KnownForDepartment} {
		switch strings.ToLower(strings.TrimSpace(d)) {
		case "directing", "导演组":
			return true
		}
	}
	return false
}

// castNames 取前 MaxCast 位主演姓名。
// castNames 取主演：按 TMDB 给出的位次（order 升序）排好，去重后取前 MaxCast 位。
// 排序这一步不能省 —— cast 数组顺序并不保证等于番位，order 才是。
func castNames(cast []person) []string {
	sorted := make([]person, len(cast))
	copy(sorted, cast)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })

	out := make([]string, 0, MaxCast)
	seen := map[string]bool{}
	for _, p := range sorted {
		n := strings.TrimSpace(p.Name)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) >= MaxCast {
			break
		}
	}
	return out
}

// structNames 把「只有名字」的条目列表摊平成字符串切片。
func structNames(list []named) []string {
	out := make([]string, 0, len(list))
	for _, it := range list {
		if n := strings.TrimSpace(it.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// uniqueNames 去重并保持原顺序，空串剔除。
func uniqueNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// joinNames 去重后拼接，keep>0 时最多保留几项。
func joinNames(names []string, sep string, keep int) string {
	out := uniqueNames(names)
	if keep > 0 && len(out) > keep {
		out = out[:keep]
	}
	return strings.Join(out, sep)
}
