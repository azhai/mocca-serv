//go:build !noweb

// 这些断言只对「带后台的构建」有意义：-tags noweb 下 public/ 整个目录
// 都没参与编译，web.FS() 按设计返回错误。没有这行标签，test-api 会红得莫名其妙。
package web_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/azhai/mocca/web"
	"github.com/labstack/echo/v5"
)

// public/ 的目录结构（改结构时同步改这里）：
//
//	index.html          浏览应用，挂 /
//	admin/index.html    管理后台，挂 /admin/
//	css/*.css           两个页面共用的样式
//	js/*.js             两个页面共用的脚本（含本地 mithril）
//	logo*.png           APP 图标
const (
	homePage  = "index.html"
	adminPage = "admin/index.html"
)

// readAsset 直接读二进制里内嵌的资源（不经 HTTP），用于在样式/脚本被拆成
// 独立文件后仍能断言「内容确实打进了二进制」。
func readAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(mustFS(t), name)
	if err != nil {
		t.Fatalf("读取内嵌资源 %q 失败: %v", name, err)
	}
	return string(b)
}

// TestHomeServedAtRoot 浏览应用是站点根：/ 必须直接给出页面。
func TestHomeServedAtRoot(t *testing.T) {
	e := echo.New()
	if err := web.Register(e, "/admin"); err != nil {
		t.Fatalf("挂载页面失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/ 状态码 = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("/ 的 Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Mocca 浏览") {
		t.Error("未返回浏览应用页面")
	}
	if strings.Contains(body, "Directory listing for") {
		t.Error("/ 返回了目录列表，首页应当由 index.html 直接提供")
	}
	// 样式与脚本必须拆成独立文件，缺一个就是白屏/裸 HTML
	for _, asset := range []string{"/css/home.css", "/css/styles.css", "/js/home.js"} {
		if !strings.Contains(body, asset) {
			t.Errorf("index.html 未引用 %s", asset)
		}
	}
}

// TestAdminServedAtAdminRoot 后台挂在 /admin/ 下，且 /admin 要被引导到规范入口。
func TestAdminServedAtAdminRoot(t *testing.T) {
	e := echo.New()
	if err := web.Register(e, "/admin"); err != nil {
		t.Fatalf("挂载页面失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/admin/ 状态码 = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("/admin/ 的 Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Mocca 管理后台") {
		t.Error("未返回后台页面")
	}
	if strings.Contains(body, "Directory listing for") {
		t.Error("/admin/ 返回了目录列表，首页应当由 admin/index.html 直接提供")
	}
	for _, asset := range []string{"/css/styles.css", "/js/app.js"} {
		if !strings.Contains(body, asset) {
			t.Errorf("admin/index.html 未引用 %s", asset)
		}
	}

	// 直接访问 admin/index.html：http.FileServer 会把它规范化重定向到 /admin/
	//（标准行为，不是 404），页面本身始终由 /admin/ 给出。
	req = httptest.NewRequest(http.MethodGet, "/admin/index.html", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("/admin/index.html 应被规范化重定向到 /admin/，got %d", rec.Code)
	}

	// 不带尾斜杠应被引导到规范入口
	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("/admin 状态码 = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/" {
		t.Errorf("Location = %q, want /admin/", loc)
	}
}

// TestStaticAssetsServed 拆出去的 css/ 与 js/ 必须能通过 HTTP 取到，
// 否则两个页面引用了绝对路径却拿到 404，就是白屏。
func TestStaticAssetsServed(t *testing.T) {
	e := echo.New()
	if err := web.Register(e, "/admin"); err != nil {
		t.Fatalf("挂载页面失败: %v", err)
	}
	for _, p := range []string{
		"/css/styles.css", "/css/home.css",
		"/js/app.js", "/js/home.js", "/js/mithril.js", "/js/static-hash.js",
		"/logo.png", "/logo-64.png",
	} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("静态资源 %s 状态码 = %d, want 200", p, rec.Code)
		}
	}

	// 目录请求不许列目录：css/ 与 js/ 里没有 index.html，FileServer 默认会列出来
	for _, p := range []string{"/css/", "/js/"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 应 404（不许列目录），got %d", p, rec.Code)
		}
	}
}

// TestAPIRoutesNotShadowed 关键守卫：静态资源挂在 /* 通配上，
// 必须确认它没有吃掉接口路由（/api/*、/d/*、/static/avatars/:key）。
// 静态/参数路由的优先级高于通配，这里把生产里真实的路由形态复刻一遍。
func TestAPIRoutesNotShadowed(t *testing.T) {
	e := echo.New()
	// 与 main.go 同序：先接口，后页面
	e.GET("/api/ping", func(c *echo.Context) error { return c.String(http.StatusOK, "pong") })
	e.GET("/static/avatars/:key", func(c *echo.Context) error {
		return c.String(http.StatusOK, "avatar:"+c.Param("key"))
	})
	e.GET("/d/*path", func(c *echo.Context) error { return c.String(http.StatusOK, "dl") })
	if err := web.Register(e, "/admin"); err != nil {
		t.Fatalf("挂载页面失败: %v", err)
	}

	cases := []struct{ path, want string }{
		{"/api/ping", "pong"},
		{"/static/avatars/abc", "avatar:abc"},
		{"/d/movie.mp4", "dl"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
			t.Errorf("%s 被静态服务器吃掉了：code=%d body=%q, want %q",
				tc.path, rec.Code, rec.Body.String(), tc.want)
		}
	}
}

// TestAssets 校验拆出去的资源本身仍被 embed 且内容没变味。
func TestAssets(t *testing.T) {
	css := readAsset(t, "css/styles.css")
	// 主题：M3 角色令牌必须在（换主题时这里会提醒同步测试口径）
	if !strings.Contains(css, "--md-primary") || !strings.Contains(css, "var(--md-surface)") {
		t.Error("css/styles.css 缺少 M3 主题令牌")
	}
	// 明确不要磨砂玻璃：M3 的分层靠色调（tonal elevation），不靠模糊。
	// 这条是反向断言 —— 防止有人又把 backdrop-filter 加回来。
	if strings.Contains(css, "backdrop-filter") {
		t.Error("不该再用 backdrop-filter，M3 用色调分层")
	}

	js := readAsset(t, "js/app.js")
	// 客户端提交口令前**必须**先做静态哈希，否则服务端会存下 bcrypt(明文)，
	// 那个账号从此永远登不进去且不报错（2026-09-17 的事故）。
	// 计数口径：调用点 3 处 —— 登录 / 保存用户 / 设置目录密码；少一处就说明有路径漏了。
	if n := strings.Count(js, "staticHash("); n < 3 {
		t.Errorf("js/app.js 里 staticHash( 只出现 %d 次，登录、保存用户与目录密码都必须先哈希", n)
	}
	// mithril 走本地 js/：后台要在局域网 http（非安全上下文）下用，
	// 一旦回到 CDN 上，断网就是白屏。
	if !strings.Contains(js, "from './mithril.js'") {
		t.Error("js/app.js 应从本地 mithril.js 导入，不能依赖 CDN")
	}
	if _, err := fs.Stat(mustFS(t), "js/mithril.js"); err != nil {
		t.Errorf("本地 mithril 未随二进制分发: %v", err)
	}

	// 静态哈希的实现单独一个文件：后台与浏览应用共用一份，
	// 定义若被搬走或删掉，两边都会静默算出不同的哈希。
	hash := readAsset(t, "js/static-hash.js")
	for _, want := range []string{"export function sha256Hex", "export async function staticHash", "alist-org/alist"} {
		if !strings.Contains(hash, want) {
			t.Errorf("js/static-hash.js 里缺少 %q", want)
		}
	}

	// 浏览应用：同样必须走本地依赖，且从共用的哈希模块拿 staticHash
	home := readAsset(t, "js/home.js")
	for _, want := range []string{"from './mithril.js'", "from './static-hash.js'", "fs/list"} {
		if !strings.Contains(home, want) {
			t.Errorf("js/home.js 里缺少 %q", want)
		}
	}
}

// TestNoRemoteDependencies 反向守卫：页面上不得出现任何远程地址。
// SALT 常量本身是个 https 字符串（非网络请求），故只匹配 src/href/import。
func TestNoRemoteDependencies(t *testing.T) {
	remote := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']https?://|from\s+["']https?://`)
	ref := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"']+)["']`)
	fsys := mustFS(t)

	for _, name := range []string{homePage, adminPage, "js/app.js", "js/home.js", "js/static-hash.js"} {
		src := readAsset(t, name)
		if loc := remote.FindString(src); loc != "" {
			t.Errorf("%s 仍引用远程地址 %q —— 必须在无外网的内网可用", name, loc)
		}
		// 引用的本地资源必须真的被 embed 了（拆文件时最易漏这个）
		for _, m := range ref.FindAllStringSubmatch(src, -1) {
			path := strings.TrimSpace(m[1])
			if path == "" || strings.HasPrefix(path, "#") || strings.Contains(path, "://") {
				continue
			}
			// 页面里写的是站点绝对路径（/css/x.css），fs 里要去掉开头的 /
			if path = strings.TrimPrefix(path, "/"); path == "" {
				continue
			}
			if _, err := fs.Stat(fsys, path); err != nil {
				t.Errorf("%s 引用了未打包的资源 %q: %v", name, path, err)
			}
		}
	}
}

func TestFSStripsPrefix(t *testing.T) {
	fsys := mustFS(t)
	// 前缀必须已剥掉：public 这层不应出现在路径里
	if _, err := fsys.Open(homePage); err != nil {
		t.Errorf("应以 %s 直接访问，got err=%v", homePage, err)
	}
	if _, err := fsys.Open("public/" + homePage); err == nil {
		t.Error("FS 仍带 public 前缀，会导致静态目录 404")
	}
	// 新结构：两个首页 + css/ + js/，少一个就是白屏或裸 HTML
	for _, name := range []string{
		homePage, adminPage,
		"css/styles.css", "css/home.css",
		"js/app.js", "js/home.js", "js/mithril.js", "js/static-hash.js",
		"logo.png", "logo-64.png",
	} {
		if _, err := fs.Stat(fsys, name); err != nil {
			t.Errorf("缺少 %s: %v", name, err)
		}
	}
	// 旧平铺结构必须已经不存在，防止有人又把文件放回 public 根。
	// 注意 admin/index.html 是新结构的正式路径，不在此列。
	// 旧结构（平铺在根）只有 app.js / mithril.js / styles.css / index.html + logo，
	// 前三个已被拆进 css/ 与 js/。
	for _, name := range []string{"styles.css", "app.js", "mithril.js"} {
		if _, err := fs.Stat(fsys, name); err == nil {
			t.Errorf("旧的 %s 仍在 public 根，应已移到 css/ 或 js/", name)
		}
	}
}

func mustFS(t *testing.T) fs.FS {
	t.Helper()
	fsys, err := web.FS()
	if err != nil {
		t.Fatalf("FS() 失败: %v", err)
	}
	return fsys
}
