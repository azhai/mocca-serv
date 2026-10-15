package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// echoHandler 一个把请求体原样回显的处理器，够用来验路由与响应封装。
func echoHandler(body string) HandlerFunc {
	return func(c *Context) error { return c.String(http.StatusOK, "%s", body) }
}

// do 发一次请求（httptest + ServeHTTP，与迁移后的仓库测试同款用法）。
func do(t *testing.T, h http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRouterPriority(t *testing.T) {
	e := New()
	e.GET("/static/avatars/:key", echoHandler("param"))
	e.GET("/css/*path", echoHandler("wild"))
	e.GET("/*", echoHandler("root-wild"))
	e.GET("/api/ping", echoHandler("ping"))

	cases := []struct{ target, want string }{
		{"/static/avatars/a3", "param"},
		{"/static/avatars/a3.png", "param"}, // 值里带点也归这一层
		{"/css/home.css", "wild"},
		{"/css/a/b/c.css", "wild"},
		{"/api/ping", "ping"}, // 静态层赢过兜底 /*
		{"/index.html", "root-wild"},
		{"/", "root-wild"},          // 兜底 /* 也接得住根
		{"/api//ping", "ping"},      // 连续斜杠不影响命中
		{"/api/ping/", "root-wild"}, // 尾斜杠是另一条路由（与 echo 一致），没注册就落到兜底 /*
		{"/nope", "root-wild"},
	}
	for _, tc := range cases {
		if rec := do(t, e, http.MethodGet, tc.target, nil); rec.Body.String() != tc.want {
			t.Errorf("GET %s = %q，想要 %q", tc.target, rec.Body.String(), tc.want)
		}
	}
}

func TestParamAndWildcardValues(t *testing.T) {
	e := New()
	var got []Param
	e.GET("/d/*path", func(c *Context) error {
		got = c.PathParams()
		return c.String(http.StatusOK, "%s", c.Param("path"))
	})
	e.POST("/u/:id/x/:name", func(c *Context) error {
		return c.String(http.StatusOK, "%s|%s", c.Param("id"), c.Param("name"))
	})

	if rec := do(t, e, http.MethodGet, "/d/media/movies/a.mp4", nil); rec.Body.String() != "media/movies/a.mp4" {
		t.Errorf("通配段 = %q，想要 media/movies/a.mp4", rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "path" {
		t.Fatalf("PathParams = %+v，想要一段名为 path", got)
	}
	// 尾斜杠不该混进路径值：播放器给的路径带不带斜杠都得是同一个文件。
	if rec := do(t, e, http.MethodGet, "/d/a/b/", nil); rec.Body.String() != "a/b" {
		t.Errorf("带尾斜杠时通配段 = %q，想要 a/b", rec.Body.String())
	}
	if rec := do(t, e, http.MethodGet, "/d/", nil); rec.Body.String() != "" {
		t.Errorf("只有前缀时通配段 = %q，想要空串", rec.Body.String())
	}
	if rec := do(t, e, http.MethodPost, "/u/7/x/bob", nil); rec.Body.String() != "7|bob" {
		t.Errorf("两个参数 = %q，想要 7|bob", rec.Body.String())
	}
	// 取不存在的参数名应回空串（handlers.downloadPath 就依赖这个行为依次试几个名字）。
	missing := false
	e.GET("/miss", func(c *Context) error {
		missing = c.Param("nope") != ""
		return c.String(http.StatusOK, "ok")
	})
	do(t, e, http.MethodGet, "/miss", nil)
	if missing {
		t.Error("取不存在的参数名应回空串")
	}
}

func TestAnyMatchesEveryMethod(t *testing.T) {
	e := New()
	e.Any("/api/ping", echoHandler("any"))
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions} {
		if rec := do(t, e, m, "/api/ping", nil); rec.Body.String() != "any" {
			t.Errorf("%s /api/ping = %q，想要 any", m, rec.Body.String())
		}
	}
}

// 方法不匹配时回 404 而不是 405：仓库里没有任何测试或客户端依赖 405（实测），
// 少一条分支就少一处行为差异。
func TestMethodMismatchIs404(t *testing.T) {
	e := New()
	e.GET("/only-get", echoHandler("get"))
	if rec := do(t, e, http.MethodPost, "/only-get", nil); rec.Code != http.StatusNotFound {
		t.Errorf("POST 命中只注册了 GET 的路径 = %d，想要 404", rec.Code)
	}
}

// 中间件顺序必须是「全局在外 > 组级 > 单条路由级」：
// main.go 把 RecoverLog/BodyLimit 挂在最外层，鉴权挂在组上，
// 顺序错了就会出现「panic 兜底接不住鉴权里抛的东西」这类问题。
func TestMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(tag string) MiddlewareFunc {
		return func(next HandlerFunc) HandlerFunc {
			return func(c *Context) error {
				order = append(order, "> "+tag)
				err := next(c)
				order = append(order, "< "+tag)
				return err
			}
		}
	}
	e := New()
	e.Use(mw("global"))
	g := e.Group("/api", mw("group"))
	sub := g.Group("", mw("sub"))
	sub.POST("/go", func(c *Context) error {
		order = append(order, "handler")
		return c.String(http.StatusOK, "ok")
	}, mw("route"))

	if rec := do(t, e, http.MethodPost, "/api/go", nil); rec.Body.String() != "ok" {
		t.Fatalf("响应 = %q", rec.Body.String())
	}
	want := strings.Join([]string{"> global", "> group", "> sub", "> route", "handler",
		"< route", "< sub", "< group", "< global"}, ",")
	if got := strings.Join(order, ","); got != want {
		t.Errorf("中间件顺序\n got %s\nwant %s", got, want)
	}
}

func TestGroupPrefixes(t *testing.T) {
	e := New()
	api := e.Group("/api")
	v1 := api.Group("/v1")
	v1.GET("/ping", echoHandler("pong"))
	e.GET("/ping", echoHandler("root"))

	if rec := do(t, e, http.MethodGet, "/api/v1/ping", nil); rec.Body.String() != "pong" {
		t.Errorf("/api/v1/ping = %q，想要 pong", rec.Body.String())
	}
	if rec := do(t, e, http.MethodGet, "/ping", nil); rec.Body.String() != "root" {
		t.Errorf("/ping = %q，想要 root", rec.Body.String())
	}
}

func TestNotFoundAndErrorHandler(t *testing.T) {
	e := New()
	e.GET("/known", echoHandler("ok"))
	if rec := do(t, e, http.MethodGet, "/unknown", nil); rec.Code != http.StatusNotFound {
		t.Errorf("未命中 = %d，想要 404", rec.Code)
	}

	boom := errors.New("处理器里的意外错误")
	e.GET("/boom", func(c *Context) error { return boom })
	rec := do(t, e, http.MethodGet, "/boom", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("处理器返回 error = %d，想要 500", rec.Code)
	}

	// 已经写过响应再返回 error：状态码不许被改写成 500（流媒体写到一半的情形）。
	e.GET("/half", func(c *Context) error {
		_ = c.String(http.StatusAccepted, "started")
		return boom
	})
	if rec := do(t, e, http.MethodGet, "/half", nil); rec.Code != http.StatusAccepted {
		t.Errorf("已写出后再报错 = %d，想要保持 202", rec.Code)
	}

	// 自定义出口也要能被换掉（后台将来若要统一错误信封只改这里）。
	e.ErrorHandler = func(c *Context, err error) { _ = c.JSON(http.StatusOK, map[string]any{"code": 500}) }
	if rec := do(t, e, http.MethodGet, "/boom", nil); rec.Code != http.StatusOK {
		t.Errorf("自定义 ErrorHandler 没生效，got %d", rec.Code)
	}
	e.NotFound = func(c *Context) error { return c.String(http.StatusTeapot, "nope") }
	if rec := do(t, e, http.MethodGet, "/unknown", nil); rec.Code != http.StatusTeapot {
		t.Errorf("自定义 NotFound 没生效，got %d", rec.Code)
	}
}

func TestContextJSONAndHeaders(t *testing.T) {
	e := New()
	e.POST("/json", func(c *Context) error {
		return c.JSON(http.StatusOK, map[string]any{"code": 200, "message": "success"})
	})
	rec := do(t, e, http.MethodPost, "/json", nil)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("JSON 的 Content-Type = %q", ct)
	}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != 200 {
		t.Errorf("信封解不出来: %v / %+v", err, body)
	}

	// 处理器自己设过 Content-Type 时不许被覆盖（头像与海报接口靠这个发 image/*）。
	e.GET("/typed", func(c *Context) error {
		c.Response().Header().Set("Content-Type", "image/svg+xml")
		return c.JSON(http.StatusOK, map[string]any{"code": 200})
	})
	if ct := do(t, e, http.MethodGet, "/typed", nil).Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("已设的 Content-Type 被覆盖成 %q", ct)
	}

	// 无额外参数时格式串原样发（有 `%` 也一样），带参数时才走 fmt。
	e.GET("/raw", func(c *Context) error { return c.String(http.StatusOK, "plain") })
	if got := do(t, e, http.MethodGet, "/raw", nil).Body.String(); got != "plain" {
		t.Errorf("无参 String = %q，想要 plain", got)
	}
	e.GET("/fmt", func(c *Context) error { return c.String(http.StatusOK, "%d%% 兼容", 100) })
	if got := do(t, e, http.MethodGet, "/fmt", nil).Body.String(); got != "100% 兼容" {
		t.Errorf("带参 String = %q，想要 100%% 兼容", got)
	}

	e.GET("/redirect", func(c *Context) error { return c.Redirect(http.StatusMovedPermanently, "/") })
	rec = do(t, e, http.MethodGet, "/redirect", nil)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/" {
		t.Errorf("Redirect = %d / %q", rec.Code, rec.Header().Get("Location"))
	}

	e.GET("/nocontent", func(c *Context) error { return c.NoContent(http.StatusNoContent) })
	if rec := do(t, e, http.MethodGet, "/nocontent", nil); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Error("NoContent 应只发状态码不写体")
	}
}

func TestContextSetGet(t *testing.T) {
	e := New()
	e.GET("/store", func(c *Context) error {
		// 没存过的键必回 nil（调用方的类型断言因此得零值，不 panic）。
		if c.Get("missing") != nil {
			t.Error("取没存过的键应回 nil")
		}
		c.Set("user_id", uint(7))
		return c.String(http.StatusOK, "ok")
	})
	e.GET("/read", func(c *Context) error {
		v, _ := c.Get("user_id").(uint) // 与 middlewares.UserID 同款断言：取错类型得零值不 panic
		return c.String(http.StatusOK, "%d", v)
	})
	if rec := do(t, e, http.MethodGet, "/store", nil); rec.Body.String() != "ok" {
		t.Fatalf("Set 那步 = %q", rec.Body.String())
	}
	// 便签是每请求的：上一个请求 Set 的东西不许漏到下一个请求。
	if rec := do(t, e, http.MethodGet, "/read", nil); rec.Body.String() != "0" {
		t.Errorf("跨请求漏了 store = %q，想要 0", rec.Body.String())
	}
}

func TestResponseIsFlusherAndWriter(t *testing.T) {
	e := New()
	e.GET("/sse", func(c *Context) error {
		w := c.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			return c.String(http.StatusInternalServerError, "not a flusher")
		}
		if _, err := w.Write([]byte("data: 1\n\n")); err != nil {
			return err
		}
		f.Flush()
		if _, err := w.Write([]byte("data: 2\n\n")); err != nil {
			return err
		}
		return nil
	})
	rec := do(t, e, http.MethodGet, "/sse", nil)
	if rec.Body.String() != "data: 1\n\ndata: 2\n\n" {
		t.Errorf("SSE 体 = %q", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Errorf("SSE 状态码 = %d", rec.Code)
	}
	if rec.Flushed {
		// httptest 只记「有没有刷过」；真正的断言是上面那条：
		// 写两段之间能 Flush 就说明包装没把 Flusher 吞掉。
		t.Log("ResponseRecorder 标记了 Flushed")
	}

	// 中间件要能把 c.Response() 直接交给 http.MaxBytesReader（BodyLimit 就靠这个）。
	e.GET("/limited", func(c *Context) error { return c.String(http.StatusOK, "body") })
	req := httptest.NewRequest(http.MethodGet, "/limited", strings.NewReader("hello"))
	rec2 := httptest.NewRecorder()
	c := &Context{e: e, req: req, res: &response{ResponseWriter: rec2}}
	req.Body = http.MaxBytesReader(c.Response(), req.Body, 1<<20)
	b, err := io.ReadAll(req.Body)
	if err != nil || string(b) != "hello" {
		t.Errorf("MaxBytesReader 包装后读取失败: %v / %q", err, b)
	}
}

func TestBindJSON(t *testing.T) {
	type req struct {
		Path string `json:"path"`
		ID   int    `json:"id"`
	}
	jsonBody := func(s string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(s))
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	bind := func(t *testing.T, r *http.Request) (req, error) {
		c := &Context{req: r, res: &response{ResponseWriter: httptest.NewRecorder()}}
		var i req
		return i, c.Bind(&i)
	}

	got, err := bind(t, jsonBody(`{"path":"/a.mp4","id":7}`))
	if err != nil || got.Path != "/a.mp4" || got.ID != 7 {
		t.Errorf("正常 JSON 绑定失败: %v / %+v", err, got)
	}
	// 空体不算错：让处理器的必填校验去回 400，而不是框架先报一句解析失败。
	if got, err := bind(t, jsonBody("")); err != nil || got.ID != 0 {
		t.Errorf("空 JSON 体 = %+v / %v，想要零值且不报错", got, err)
	}
	if _, err := bind(t, jsonBody(`{"path":`)); err == nil {
		t.Error("坏 JSON 必须报错")
	}
	// +json 后缀与带 charset 的写法都得认。
	r := jsonBody(`{"id":3}`)
	r.Header.Set("Content-Type", "application/merge-patch+json; charset=utf-8")
	if got, err := bind(t, r); err != nil || got.ID != 3 {
		t.Errorf("+json 绑定失败: %v / %+v", err, got)
	}
}

func TestBindFormAndQuery(t *testing.T) {
	type req struct {
		Name string   `json:"name"`
		ID   int      `json:"id"`
		Tags []string `json:"tags"`
	}
	bind := func(t *testing.T, r *http.Request) (req, error) {
		c := &Context{req: r, res: &response{ResponseWriter: httptest.NewRecorder()}}
		var i req
		return i, c.Bind(&i)
	}

	// urlencoded 表单。
	form := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=bob&id=5"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got, err := bind(t, form); err != nil || got.Name != "bob" || got.ID != 5 {
		t.Errorf("表单绑定失败: %v / %+v", err, got)
	}

	// 查询串：数字进 int 字段（第一遍按原样 JSON 嵌）。
	q := httptest.NewRequest(http.MethodGet, "/x?name=bob&id=5&tags=a&tags=b", nil)
	if got, err := bind(t, q); err != nil || got.ID != 5 || len(got.Tags) != 2 {
		t.Errorf("查询串绑定失败: %v / %+v", err, got)
	}

	// 同名值像数字但字段是 string：第一遍会失败，第二遍全按字符串解要成功。
	str := httptest.NewRequest(http.MethodGet, "/x?name=123", nil)
	if got, err := bind(t, str); err != nil || got.Name != "123" {
		t.Errorf("数字样字符串进 string 字段失败: %v / %+v", err, got)
	}

	// 没带任何数据：不报错，留零值。
	if got, err := bind(t, httptest.NewRequest(http.MethodGet, "/x", nil)); err != nil || got.Name != "" {
		t.Errorf("空查询串 = %+v / %v", got, err)
	}
}

func TestFormValueAndMultipart(t *testing.T) {
	e := New()
	e.POST("/fv", func(c *Context) error { return c.String(http.StatusOK, "%s", c.FormValue("name")) })
	e.POST("/mf", func(c *Context) error {
		mf, err := c.MultipartForm()
		if err != nil {
			return err
		}
		f := mf.File["upload"]
		if len(f) != 1 {
			return c.String(http.StatusOK, "no-file")
		}
		return c.String(http.StatusOK, "%s:%s", f[0].Filename, mf.Value["name"][0])
	})

	form := httptest.NewRequest(http.MethodPost, "/fv", strings.NewReader("name=bob"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, form)
	if rec.Body.String() != "bob" {
		t.Errorf("FormValue 读不到 urlencoded 表单: %q", rec.Body.String())
	}
	if rec := do(t, e, http.MethodPost, "/fv?name=fromquery", nil); rec.Body.String() != "fromquery" {
		t.Errorf("FormValue 没回落到查询串: %q", rec.Body.String())
	}

	mr := httptest.NewRequest(http.MethodPost, "/mf", strings.NewReader(
		"--B\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nmia\r\n"+
			"--B\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"a.mp4\"\r\n"+
			"Content-Type: video/mp4\r\n\r\nx\r\n--B--\r\n"))
	mr.Header.Set("Content-Type", "multipart/form-data; boundary=B")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, mr)
	if rec.Body.String() != "a.mp4:mia" {
		t.Errorf("multipart 解析结果 = %q，想要 a.mp4:mia", rec.Body.String())
	}

	// 不是 multipart 却调 MultipartForm：必须明确报错而不是 nil 崩。
	e.GET("/notmf", func(c *Context) error {
		if _, err := c.MultipartForm(); errors.Is(err, ErrNotMultipart) {
			return c.String(http.StatusOK, "rejected")
		}
		return c.String(http.StatusOK, "unexpected")
	})
	if rec := do(t, e, http.MethodGet, "/notmf", nil); rec.Body.String() != "rejected" {
		t.Errorf("非 multipart 调 MultipartForm = %q", rec.Body.String())
	}
}

// Routes() 的形状是迁移后 routes_test 断言构建差异的依据：
// 路径要含组前缀，Any 记作 "*"。
func TestRoutesRegistry(t *testing.T) {
	e := New()
	e.GET("/ping", echoHandler("pong"))
	api := e.Group("/api")
	api.POST("/fs/list", echoHandler("l"))
	api.Any("/ping", echoHandler("p"))
	d := e.Group("")
	d.GET("/d/*path", echoHandler("stream"))

	have := map[string]bool{}
	for _, r := range e.Router().Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{"GET /ping", "POST /api/fs/list", "* /api/ping", "GET /d/*path"} {
		if !have[want] {
			t.Errorf("登记表里缺 %q，实有 %+v", want, e.Router().Routes())
		}
	}
}

// 同一方法同一路径重复注册：最后一次赢（手写路由表里重复是笔误，不该中断启动）。
func TestReRegisterKeepsLast(t *testing.T) {
	e := New()
	e.GET("/x", echoHandler("first"))
	e.GET("/x", echoHandler("second"))
	if rec := do(t, e, http.MethodGet, "/x", nil); rec.Body.String() != "second" {
		t.Errorf("重复注册后 = %q，想要 second", rec.Body.String())
	}
	if n := len(e.Router().Routes()); n != 2 {
		t.Errorf("登记表应记两条（各一次注册），got %d", n)
	}
}
