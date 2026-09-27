package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGuessTitle 文件名 → 关键词/年份。真实库里的名字什么写法都有，逐类钉住。
func TestGuessTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
		year  int
	}{
		{"Interstellar.2014.1080p.BluRay.x264.mp4", "Interstellar", 2014},
		{"让子弹飞(2010).1080p.mp4", "让子弹飞", 2010},
		{"[高清] 星际穿越 2014.mp4", "星际穿越", 2014},
		{"Spider-Man_No_Way_Home.2021.mkv", "Spider-Man No Way Home", 2021},
		{"[阳光电影www.ygdy8.com]盗梦空间.Inception.2010.BD-1080p.mkv", "盗梦空间 Inception", 2010},
		{"The.Matrix.1999.Remux.HEVC.TrueHD.Atmos.mkv", "The Matrix", 1999},
		// 年份只有独立成词才算：贴着片名的那串数字是片名的一部分
		{"2001太空漫游.mkv", "2001太空漫游", 0},
		{"2001.A.Space.Odyssey.1968.1080p.mkv", "2001 A Space Odyssey", 1968},
		{"a.mp4", "a", 0},
		// 整名都是发布标记：退回原始主名，关键词不能是空串
		{"1080p_BDRip.mkv", "1080p BDRip", 0},
	}
	for _, c := range cases {
		got, year := GuessTitle(c.name)
		if got != c.title || year != c.year {
			t.Errorf("GuessTitle(%q) = (%q, %d)，want (%q, %d)", c.name, got, year, c.title, c.year)
		}
	}
}

// TestGuessTitleDashYear 短横年份必须拆开：`让子弹飞-2010` 之前整个成了关键词
// （短横为保住 Spider-Man 而保留，于是连年份都认不出来），TMDB 自然搜不到。
func TestGuessTitleDashYear(t *testing.T) {
	cases := []struct {
		name  string
		title string
		year  int
	}{
		{"让子弹飞-2010.mkv", "让子弹飞", 2010},
		{"让子弹飞-2010-1080p.mkv", "让子弹飞", 2010},
		{"Interstellar-2014.1080p.mkv", "Interstellar", 2014},
		// 短横属于片名的一部分时不许乱切
		{"Spider-Man.mkv", "Spider-Man", 0},
		{"Spider-Man 2002.mkv", "Spider-Man", 2002},
	}
	for _, c := range cases {
		got, year := GuessTitle(c.name)
		if got != c.title || year != c.year {
			t.Errorf("GuessTitle(%q) = (%q, %d)，want (%q, %d)", c.name, got, year, c.title, c.year)
		}
	}
}

// TestSplitKeyword 用户手填的检索词要拆成「片名 + 年份」：年份留在关键词里几乎必然搜不到，
// 得作为 year 参数下发。只认末尾的「空格+年份」「短横+年份」两种写法。
func TestSplitKeyword(t *testing.T) {
	cases := []struct {
		in    string
		title string
		year  int
	}{
		{"无间道 2002", "无间道", 2002},
		{"无间道-2002", "无间道", 2002},
		{"The Matrix 1999", "The Matrix", 1999},
		{"无间道", "无间道", 0},
		// 片名里的数字不是年份：不拆
		{"2001太空漫游", "2001太空漫游", 0},
		{"2001 A Space Odyssey", "2001 A Space Odyssey", 0},
		// 末尾数字不像年份时不认
		{"无间道 0001", "无间道 0001", 0},
	}
	for _, c := range cases {
		got, year := SplitKeyword(c.in)
		if got != c.title || year != c.year {
			t.Errorf("SplitKeyword(%q) = (%q, %d)，want (%q, %d)", c.in, got, year, c.title, c.year)
		}
	}
}

// TestSearchRanksExactTitleThenYear 检索结果的排序：完全符合片名的排在最前，同档内年份相符的靠前。
// 前端默认填充取的就是排序后的第一条，刮得准不准全看这一步。
func TestSearchRanksExactTitleThenYear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			w.Write([]byte(`{}`))
			return
		}
		// 不论带不带年份都回同一份列表，且刻意把「部分符合」的排在 TMDB 相关度最前
		w.Write([]byte(`{"results":[
			{"id":10,"title":"无间道风云","original_title":"The Departed","release_date":"2006-10-06"},
			{"id":11,"title":"无间道","original_title":"無間道","release_date":"2002-12-12"},
			{"id":12,"title":"无间道","original_title":"無間道","release_date":"2003-10-01"}]}`))
	}))
	defer srv.Close()
	oldBase := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = oldBase }()

	c, err := New("v3key", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		year int
		want []int
	}{
		{2002, []int{11, 12, 10}}, // 同名的两条里，年份相符的 11 靠前
		{2003, []int{12, 11, 10}}, // 换一个年份，顺序跟着换
		{0, []int{11, 12, 10}},    // 不给年份时，完全相符的仍排在部分相符的前面
	} {
		list, err := c.Search(context.Background(), "无间道", tc.year)
		if err != nil {
			t.Fatalf("year=%d 检索失败: %v", tc.year, err)
		}
		got := make([]int, 0, len(list))
		for _, m := range list {
			got = append(got, m.ID)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("year=%d 候选数 = %d，want %d", tc.year, len(got), len(tc.want))
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("year=%d 排序 = %v，want %v", tc.year, got, tc.want)
				break
			}
		}
	}
}

// fakeTMDB 起一个假 TMDB：/search/movie、/movie/<id>、以及图片路径都覆盖。
// 校验 v3 密钥（api_key 查询串）与 v4 读令牌（Authorization: Bearer）两条鉴权路径。
func fakeTMDB(t *testing.T, wantBearer bool) (*httptest.Server, *[]string) {
	t.Helper()
	seen := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 鉴权与语言只对 API 路径成立：图片请求走 ImageBase，既不带密钥也不带语言
		if strings.HasPrefix(r.URL.Path, "/search/") || strings.HasPrefix(r.URL.Path, "/movie/") {
			if wantBearer {
				if got := r.Header.Get("Authorization"); got != "Bearer k1.k2.k3" {
					t.Errorf("Authorization = %q，want Bearer k1.k2.k3", got)
				}
				if r.URL.Query().Get("api_key") != "" {
					t.Error("v4 读令牌不该再走 api_key 查询串")
				}
			} else if r.URL.Query().Get("api_key") != "v3key" {
				t.Errorf("api_key = %q，want v3key", r.URL.Query().Get("api_key"))
			}
			if lang := r.URL.Query().Get("language"); lang != DefaultLang {
				t.Errorf("language = %q，want %s", lang, DefaultLang)
			}
		}
		*seen = append(*seen, r.URL.Path)

		switch {
		case r.URL.Path == "/search/movie":
			if q := r.URL.Query().Get("query"); q != "Interstellar" {
				t.Errorf("query = %q，want Interstellar", q)
			}
			// 带年份查不到时客户端会去掉年份重查，这里借此模拟「年份猜错」的场景
			if r.URL.Query().Get("year") != "" {
				w.Write([]byte(`{"results":[]}`))
				return
			}
			w.Write([]byte(`{"results":[{"id":157336,"title":"星际穿越","original_title":"Interstellar",
				"overview":"地球濒临毁灭。","poster_path":"/abc.jpg","backdrop_path":"/bd.jpg",
				"release_date":"2014-11-05","vote_average":8.4}]}`))
		case r.URL.Path == "/movie/157336":
			if got := r.URL.Query().Get("append_to_response"); got != "credits" {
				t.Errorf("append_to_response = %q，want credits", got)
			}
			w.Write([]byte(`{"id":157336,"title":"星际穿越","original_title":"Interstellar",
				"overview":"地球濒临毁灭。","poster_path":"/abc.jpg","backdrop_path":"/bd.jpg",
				"release_date":"2014-11-05",
				"runtime":169,"vote_average":8.4,
				"genres":[{"name":"冒险"}],"production_companies":[{"name":"Legendary Pictures"}],
				"production_countries":[{"name":"美国"}],"spoken_languages":[{"name":"英语"}],
				"credits":{"cast":[{"name":"马修·麦康纳"},{"name":"安妮·海瑟薇"}],
				           "crew":[{"name":"克里斯托弗·诺兰","job":"Director"},
				                   {"name":"汉斯·季默","job":"Original Music Composer"}]}}`))
		default:
			w.Write([]byte("fake-image-bytes"))
		}
	}))
	t.Cleanup(srv.Close)

	oldBase, oldImg := BaseURL, ImageBase
	BaseURL, ImageBase = srv.URL, srv.URL+"/img"
	t.Cleanup(func() { BaseURL, ImageBase = oldBase, oldImg })
	return srv, seen
}

func TestClientSearchFallsBackWithoutYear(t *testing.T) {
	_, seen := fakeTMDB(t, false)
	c, err := New("v3key", "", "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.Search(context.Background(), "Interstellar", 2013) // 年份故意猜错
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应有 1 条候选，got %d", len(list))
	}
	got := list[0]
	if got.ID != 157336 || got.Title != "星际穿越" || got.Year != 2014 {
		t.Errorf("候选字段不符: %+v", got)
	}
	if !strings.HasSuffix(got.Poster, "/w500/abc.jpg") {
		t.Errorf("海报地址 = %q，want 以 /w500/abc.jpg 结尾", got.Poster)
	}
	// 候选也要带剧照：前端候选区是「横版一行 + 竖版一行」，横版那行的图源就是它，
	// 少了这个字段前端只能空着一行。
	if !strings.HasSuffix(got.Backdrop, "/w780/bd.jpg") {
		t.Errorf("剧照地址 = %q，want 以 /w780/bd.jpg 结尾", got.Backdrop)
	}
	// 带年份一次、不带年份一次：证明「查不到就去掉年份重查」
	if len(*seen) != 2 {
		t.Errorf("应请求两次检索，got %d 次: %v", len(*seen), *seen)
	}
}

func TestClientMovieMapsFields(t *testing.T) {
	fakeTMDB(t, true)
	c, err := New("k1.k2.k3", "", "") // v4 读令牌形态 → 走 Bearer
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Movie(context.Background(), 157336)
	if err != nil {
		t.Fatalf("取详情失败: %v", err)
	}
	if m.Title != "星际穿越" || m.Year != 2014 || m.Runtime != 169 {
		t.Errorf("基础字段不符: %+v", m)
	}
	if m.Director != "克里斯托弗·诺兰" {
		t.Errorf("导演应只取 Director 职务，got %q", m.Director)
	}
	if len(m.Cast) != 2 || m.Cast[0] != "马修·麦康纳" {
		t.Errorf("主演不符: %v", m.Cast)
	}
	if m.Region != "美国" || m.Studio != "Legendary Pictures" || m.Language != "英语" {
		t.Errorf("地区/出品方/语言不符: %q %q %q", m.Region, m.Studio, m.Language)
	}
	if m.PosterPath != "/abc.jpg" {
		t.Errorf("PosterPath = %q，want /abc.jpg（下载海报要用它）", m.PosterPath)
	}
	// 剧照要单独带出来：封面优先用它（16:9 横版裁成 4:3 只切左右各约 12.5%，
	// 而竖版海报会被上下砍掉一半 —— 见 handlers.ScrapeApply）
	if m.BackdropPath != "/bd.jpg" {
		t.Errorf("BackdropPath = %q，want /bd.jpg", m.BackdropPath)
	}
	if !strings.HasSuffix(m.Backdrop, "/w780/bd.jpg") {
		t.Errorf("剧照地址 = %q，want 以 /w780/bd.jpg 结尾（剧照另有尺寸档）", m.Backdrop)
	}

	// 海报下载：假服务把任何非 API 路径都当图片
	raw, err := c.FetchImage(context.Background(), m.PosterPath)
	if err != nil {
		t.Fatalf("下载海报失败: %v", err)
	}
	if string(raw) != "fake-image-bytes" {
		t.Errorf("海报字节 = %q", raw)
	}
	// 剧照下载要按 BackdropSize 拼地址（取错尺寸就拿不到图）
	if _, err := c.FetchImage(context.Background(), m.BackdropPath, BackdropSize); err != nil {
		t.Fatalf("下载剧照失败: %v", err)
	}
}

// TestClientNeedsKeyAndSurfacesUpstreamError 未配密钥与上游报错都要给可读的中文原因。
func TestClientNeedsKeyAndSurfacesUpstreamError(t *testing.T) {
	if _, err := New("  ", "", ""); err != ErrNoAPIKey {
		t.Errorf("空密钥应返回 ErrNoAPIKey，got %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"status_message": "Invalid API key"})
	}))
	defer srv.Close()
	oldBase := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = oldBase }()

	c, err := New("bad", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "x", 0); err == nil {
		t.Fatal("上游 401 应当报错")
	} else if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("错误里应带上游的 status_message，got %v", err)
	}
}

// TestClientUsesProxy 配了代理就必须走代理：目标是 TMDB 直连不通时，这是唯一的出路。
// 断言两件事：代理确实收到了请求（且是绝对形式的目标地址），目标没有被直连。
func TestClientUsesProxy(t *testing.T) {
	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.URL.String())
		_, _ = w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	defer proxy.Close()

	var direct int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct++
		_, _ = w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	defer target.Close()

	oldBase := BaseURL
	BaseURL = target.URL // 用 http 目标：https 会走 CONNECT 隧道，断言起来更绕
	defer func() { BaseURL = oldBase }()

	c, err := New("v3key", "", proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "x", 0); err != nil {
		t.Fatalf("经代理请求失败: %v", err)
	}
	if len(proxied) != 1 {
		t.Fatalf("代理应收到 1 次请求，实得 %d 次", len(proxied))
	}
	// 走代理时目标地址以绝对形式出现在请求行里
	if !strings.HasPrefix(proxied[0], target.URL) {
		t.Errorf("代理收到的应是绝对形式的目标地址，got %q", proxied[0])
	}
	if direct != 0 {
		t.Errorf("不该绕过代理直连目标，直连 %d 次", direct)
	}
}

// TestNewRejectsBadProxy 代理地址写错要在构造时就说清楚，而不是等点了刮削才报一句难懂的错。
func TestNewRejectsBadProxy(t *testing.T) {
	for _, bad := range []string{"127.0.0.1:7890", "://bad", "http://"} {
		if _, err := New("v3key", "", bad); err == nil {
			t.Errorf("非法代理地址 %q 应当报错", bad)
		}
	}
	// 合法的 http 代理不该被误伤
	if _, err := New("v3key", "", "http://127.0.0.1:7890"); err != nil {
		t.Errorf("合法代理地址不该报错: %v", err)
	}
}

// TestErrorRedactsKeyAndMentionsProxy 失败信息里既不能泄漏密钥，也要说清走没走代理。
//
// 回归自一次真实事故：v3 密钥以 ?api_key= 挂在 URL 上，*url.Error 会把它原样带出来，
// 于是后台界面把完整密钥显示给了用户（也就顺手写进了日志）。
// 顺带钉住第二件事：TLS/超时类失败必须能看出代理有没有生效，否则排障只能猜。
func TestErrorRedactsKeyAndMentionsProxy(t *testing.T) {
	const key = "096bc0791a1f13e17a5e1ed187a5ef7e"
	oldBase := BaseURL
	BaseURL = "http://127.0.0.1:1" // 必然连不上：用来稳定复现失败路径
	defer func() { BaseURL = oldBase }()

	// 直连：错误里要说「未走代理」
	c, err := New(key, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, serr := c.Search(context.Background(), "x", 0)
	if serr == nil {
		t.Fatal("连不上应当报错")
	}
	msg := serr.Error()
	if strings.Contains(msg, key) {
		t.Errorf("错误信息泄漏了密钥: %s", msg)
	}
	if !strings.Contains(msg, "api_key=***") {
		t.Errorf("api_key 应被打码，got %s", msg)
	}
	if !strings.Contains(msg, "未走代理") {
		t.Errorf("应说明未走代理，got %s", msg)
	}

	// 配了代理：错误里要说清用的是哪个代理
	c2, err := New(key, "", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	_, serr = c2.Search(context.Background(), "x", 0)
	if serr == nil {
		t.Fatal("代理连不上应当报错")
	}
	msg = serr.Error()
	if !strings.Contains(msg, "代理 http://127.0.0.1:1") {
		t.Errorf("应说明所用代理，got %s", msg)
	}
	if strings.Contains(msg, key) {
		t.Errorf("错误信息泄漏了密钥: %s", msg)
	}
}

// TestMovieFallsBackToCreditsEndpoint 详情里的 append_to_response 没带回演职员时，
// 必须改用独立端点 /movie/<id>/credits 兜底（golang-tmdb 就是把 credits 当独立端点用的）。
// 少了这一步的表现正是用户报的「简介年份都有、导演和主演却是空的」。
func TestMovieFallsBackToCreditsEndpoint(t *testing.T) {
	var creditCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/157336":
			// 详情里完全不带 credits：模拟 append_to_response 失效的环境
			w.Write([]byte(`{"id":157336,"title":"星际穿越","release_date":"2014-11-05","overview":"简介"}`))
		case "/movie/157336/credits":
			creditCalls++
			w.Write([]byte(`{"id":157336,
				"cast":[{"name":"马修·麦康纳","order":1},{"name":"安妮·海瑟薇","order":0}],
				"crew":[{"name":"克里斯托弗·诺兰","job":"Director","department":"Directing"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	oldBase := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = oldBase }()

	c, err := New("v3key", "", "")
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Movie(context.Background(), 157336)
	if err != nil {
		t.Fatalf("取详情失败: %v", err)
	}
	if creditCalls != 1 {
		t.Errorf("应兜底请求一次独立端点，实得 %d 次", creditCalls)
	}
	if m.Director != "克里斯托弗·诺兰" {
		t.Errorf("导演 = %q", m.Director)
	}
	// 按 order 排序：安妮(order 0) 应在马修(order 1) 之前
	if len(m.Cast) != 2 || m.Cast[0] != "安妮·海瑟薇" || m.Cast[1] != "马修·麦康纳" {
		t.Errorf("主演应按 order 排序，got %v", m.Cast)
	}
}

// TestDirectorNamesVariants 认导演要稳：中英文写法、职务缺失都得认出来；
// 但「助理导演」绝不能算成导演。
func TestDirectorNamesVariants(t *testing.T) {
	for _, tc := range []struct {
		name string
		crew []person
		want []string
	}{
		{"职务 Director", []person{{Name: "诺兰", Job: "Director"}}, []string{"诺兰"}},
		{"中文职务", []person{{Name: "张艺谋", Job: "导演"}}, []string{"张艺谋"}},
		{"联合导演", []person{{Name: "科恩", Job: "Co-Director"}}, []string{"科恩"}},
		{"职务缺失但有部门", []person{{Name: "诺兰", Department: "Directing"}}, []string{"诺兰"}},
		// 助理导演：职务填了却不是导演，不该被算进来
		{"助理导演不算", []person{{Name: "助理", Job: "Assistant Director"}}, nil},
		{"无关职务不算", []person{{Name: "摄影", Job: "Director of Photography"}}, nil},
		{"多人联合执导", []person{{Name: "甲", Job: "Director"}, {Name: "乙", Job: "Director"}}, []string{"甲", "乙"}},
	} {
		got := directorNames(tc.crew)
		if len(got) != len(tc.want) {
			t.Errorf("%s: directorNames = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: directorNames = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

// TestCastNamesOrderedCappedDeduped 主演按位次排序、去重、且不超过 MaxCast。
func TestCastNamesOrderedCappedDeduped(t *testing.T) {
	cast := []person{
		{Name: "丙", Order: 5}, {Name: "甲", Order: 0},
		{Name: "甲", Order: 3}, // 重名，应被去掉
		{Name: "", Order: 1},  // 空名跳过
		{Name: "乙", Order: 2},
	}
	got := castNames(cast)
	want := []string{"甲", "乙", "丙"}
	if len(got) != len(want) {
		t.Fatalf("castNames = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("castNames = %v, want %v", got, want)
		}
	}
	// 上限：多于 MaxCast 时只留前 MaxCast 位
	many := make([]person, 0, MaxCast+5)
	for i := 0; i < MaxCast+5; i++ {
		many = append(many, person{Name: fmt.Sprintf("演员%d", i), Order: i})
	}
	if got := castNames(many); len(got) != MaxCast {
		t.Errorf("主演应最多 %d 位，got %d", MaxCast, len(got))
	}
}

// TestRedactUserInfo 代理 URL 里的账号口令不能出现在错误信息里。
func TestRedactUserInfo(t *testing.T) {
	got := redactUserInfo("http://user:pass@127.0.0.1:7890")
	if strings.Contains(got, "pass") || strings.Contains(got, "user") {
		t.Errorf("账号口令未被去掉: %q", got)
	}
	if !strings.Contains(got, "127.0.0.1:7890") {
		t.Errorf("主机端口应保留: %q", got)
	}
	if got := redactUserInfo("http://127.0.0.1:7890"); got != "http://127.0.0.1:7890" {
		t.Errorf("没有账号口令时应原样返回: %q", got)
	}
}
