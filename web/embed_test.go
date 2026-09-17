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

// readAsset 直接读二进制里内嵌的资源（不经 HTTP），用于在样式/脚本被拆成
// 独立文件后仍能断言「内容确实打进了二进制」。
func readAsset(t *testing.T, name string) string {
	t.Helper()
	fsys, err := web.FS()
	if err != nil {
		t.Fatalf("FS() 失败: %v", err)
	}
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("读取内嵌资源 %q 失败: %v", name, err)
	}
	return string(b)
}

// TestAdminServesEmbeddedIndex 验证后台确实被编译进二进制：
// 若 embed 路径写错或目录为空，这里会立刻失败（而不是上线后才发现白屏）。
func TestAdminServesEmbeddedIndex(t *testing.T) {
	e := echo.New()
	if err := web.Register(e, "/admin"); err != nil {
		t.Fatalf("挂载后台失败: %v", err)
	}

	// 注意：/admin/index.html 会被 http.FileServer 规范化重定向到 /admin/（301），
	// 这是标准库行为而非路由问题，因此只断言目录入口。
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/admin/ 状态码 = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Mocca 管理后台") {
		t.Error("未返回后台页面")
	}
	// 样式与脚本必须拆成独立文件，缺一个就是白屏/裸 HTML
	for _, asset := range []string{"styles.css", "app.js"} {
		if !strings.Contains(body, asset) {
			t.Errorf("index.html 未引用 %s", asset)
		}
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

// TestAdminAssets 校验拆出去的资源本身仍被 embed 且内容没变味。
// 早先这些断言是打在 index.html 返回的 body 上的，拆文件后如果不同步改口径，
// 断言就会静默失效（永远绿）——这里按文件逐个读回来。
func TestAdminAssets(t *testing.T) {
	css := readAsset(t, "styles.css")
	// 主题：M3 角色令牌必须在（换主题时这里会提醒同步测试口径）
	if !strings.Contains(css, "--md-primary") || !strings.Contains(css, "var(--md-surface)") {
		t.Error("styles.css 缺少 M3 主题令牌")
	}
	// 明确不要磨砂玻璃：M3 的分层靠色调（tonal elevation），不靠模糊。
	// 这条是反向断言 —— 防止有人又把 backdrop-filter 加回来。
	if strings.Contains(css, "backdrop-filter") {
		t.Error("不该再用 backdrop-filter，M3 用色调分层")
	}

	js := readAsset(t, "app.js")
	// 客户端提交口令前**必须**先做静态哈希，否则服务端会存下 bcrypt(明文)，
	// 那个账号从此永远登不进去且不报错（2026-09-17 的事故）。
	// 计数口径：函数定义 1 处 + 登录 1 处 + 保存用户 1 处；少一处就说明有路径漏了。
	if n := strings.Count(js, "staticHash("); n < 3 {
		t.Errorf("staticHash( 只出现 %d 次，登录与保存用户的口令都必须先哈希", n)
	}
	// mithril 走本地 lib/：后台要在局域网 http（非安全上下文）下用，
	// 一旦回到 CDN 上，断网就是白屏。
	if !strings.Contains(js, "from './mithril.js'") {
		t.Error("app.js 应从本地 mithril.js 导入，不能依赖 CDN")
	}
	if _, err := fs.Stat(mustFS(t), "mithril.js"); err != nil {
		t.Errorf("本地 mithril 未随二进制分发: %v", err)
	}
}

// TestAdminNoRemoteDependencies 反向守卫：页面上不得出现任何远程地址。
// SALT 常量本身是个 https 字符串（非网络请求），故只匹配 src/href/import。
func TestAdminNoRemoteDependencies(t *testing.T) {
	remote := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']https?://|from\s+["']https?://`)
	ref := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"']+)["']`)
	fsys := mustFS(t)

	for _, name := range []string{"index.html", "app.js"} {
		src := readAsset(t, name)
		if loc := remote.FindString(src); loc != "" {
			t.Errorf("%s 仍引用远程地址 %q —— 后台必须在无外网的内网可用", name, loc)
		}
		// 引用的本地资源必须真的被 embed 了（拆文件时最易漏这个）
		for _, m := range ref.FindAllStringSubmatch(src, -1) {
			path := strings.TrimSpace(m[1])
			if path == "" || strings.HasPrefix(path, "#") || strings.Contains(path, "://") {
				continue
			}
			if _, err := fs.Stat(fsys, path); err != nil {
				t.Errorf("%s 引用了未打包的资源 %q: %v", name, path, err)
			}
		}
	}
}

func TestAdminFSStripsPrefix(t *testing.T) {
	fsys, err := web.FS()
	if err != nil {
		t.Fatalf("FS() 失败: %v", err)
	}
	// 前缀必须已剥掉：public 这层不应出现在路径里
	if _, err := fsys.Open("index.html"); err != nil {
		t.Errorf("应以 index.html 直接访问，got err=%v", err)
	}
	if _, err := fsys.Open("public/index.html"); err == nil {
		t.Error("FS 仍带 public 前缀，会导致静态目录 404")
	}
	// 拆出去的静态资源同样要在这一层，否则浏览器 404
	for _, name := range []string{"styles.css", "app.js", "mithril.js"} {
		if _, err := fs.Stat(fsys, name); err != nil {
			t.Errorf("缺少 %s: %v", name, err)
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
