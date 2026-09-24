//go:build !noweb

// Package web 把管理后台打包进二进制。
//
// 关键约定：embed 只能嵌入「当前包目录及其子目录」里的文件，
// 所以后台静态文件必须放在本包下的 public/，不能留在仓库根目录。
//
// 目录名不用 vendor/：仓库 .gitignore 里有 vendor/ 规则，第三方库文件放
// 进去会被 git 忽略掉，别人 clone 下来就编不出同一个二进制。
//
// 本文件只在**带管理后台**的构建里编译。纯 API 构建（-tags noweb）
// 走同包的 noweb.go：资源不参与编译，Register 是空操作。
package web

import (
	"bytes"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

// Embedded 当前构建是否带管理后台。main 靠它决定要不要挂载 /admin、
// 以及启动日志里怎么自述版本；同名常量也定义在 noweb.go 里（值为 false）。
const Embedded = true

//go:embed all:public
var content embed.FS

// FS 返回后台文件系统（已剥掉 public 这层前缀）。
func FS() (fs.FS, error) {
	return fs.Sub(content, "public")
}

// serveFS 决定后台静态资源从哪来：
//   - 默认：编译进二进制的 embed.FS（生产，运行时不依赖外部目录）。
//   - 开发：设 MOCCA_SERVE_DISK=1（或给目录路径）时，直接从磁盘读 public/，
//     改完 CSS/JS 刷新即可见，不用每次重新编译二进制。
//
// 两种情况返回的都是「以 public 为根」的 fs.FS，所以 index.html 与
// admin/index.html 无论走哪条路都是同一份页面。
func serveFS() (fs.FS, error) {
	if v := os.Getenv("MOCCA_SERVE_DISK"); v != "" {
		root := "public"
		if v != "1" && v != "true" {
			root = v
		}
		return fs.Sub(os.DirFS(root), ".")
	}
	return FS()
}

// diskMode 是否从磁盘读 public/（开发模式）。磁盘内容随时会变，不能缓存。
func diskMode() bool { return os.Getenv("MOCCA_SERVE_DISK") != "" }

// asset 一条静态资源：内容常驻内存 + 一个按内容算出来的 ETag。
// 资源总共几百 KB，读一次存住比每次请求重读划算。
type asset struct {
	body []byte
	etag string
}

var (
	assetMu    sync.RWMutex
	assetCache = map[string]*asset{}
)

// loadAsset 取（必要时缓存）某条资源的内容与 ETag。
// immutable 为真才缓存：内嵌资源编译后就固定不变，磁盘模式下的文件随时会改。
func loadAsset(fsys fs.FS, name string, immutable bool) (*asset, error) {
	if immutable {
		assetMu.RLock()
		a, ok := assetCache[name]
		assetMu.RUnlock()
		if ok {
			return a, nil
		}
	}
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(b)
	a := &asset{body: b, etag: `"` + hex.EncodeToString(sum[:]) + `"`}
	if immutable {
		assetMu.Lock()
		assetCache[name] = a
		assetMu.Unlock()
	}
	return a, nil
}

// Register 挂载浏览应用与后台。二进制自带资源，运行时不依赖任何外部目录。
//
// 页面入口：
//
//	/             浏览应用（public/index.html）
//	prefix + "/"  管理后台（public/admin/index.html，prefix 即 /admin）
//
// 静态资源（/css/*、/js/*、/logo*.png）由文件服务器从 public 根直接提供：
// public 就是站点根，所以不需要 StripPrefix；两个页面都用绝对路径引用资源，
// 于是 / 与 /admin/ 下取到的是同一份文件。
func Register(e *echo.Echo, prefix string) error {
	// 资源来源：默认 embed，开发期可设 MOCCA_SERVE_DISK 从磁盘读（见 serveFS）。
	fsys, err := serveFS()
	if err != nil {
		return err
	}

	// 两个首页都只有几百字节：读一次常驻内存直接发。
	// 由服务端直接给出，也顺带堵掉了目录列表那条路 —— 静态目录里没有
	// index.html 时 http.FileServer 会把内部文件全列出来。
	indexHTML, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return err
	}
	adminHTML, err := fs.ReadFile(fsys, "admin/index.html")
	if err != nil {
		return err
	}
	writeHTML := func(body []byte) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Response().Header()
			h.Set("Content-Type", "text/html; charset=utf-8")
			// 页面引用的是 /js/home.js 这种固定地址，页面本身被缓存住就会一直
			// 指向同一份旧脚本 —— 每次回源校验，成本只有一个 1KB 的 200。
			h.Set("Cache-Control", "no-cache")
			_, err := c.Response().Write(body)
			return err
		}
	}

	// 浏览应用：站点根。
	e.GET("/", writeHTML(indexHTML))

	// 后台：不带尾斜杠时重定向到规范入口。
	e.GET(prefix, func(c *echo.Context) error {
		return c.Redirect(http.StatusFound, prefix+"/")
	})
	e.GET(prefix+"/", writeHTML(adminHTML))

	// index.html 的地址规范到目录形式入口。这原是 http.FileServer 的规范化行为，
	// 换成自己走 ServeContent 后得显式补上，否则同一份页面会有两个地址、各自可被缓存。
	// （两个页面都用绝对路径引用资源，所以跳不跳对资源加载都没影响。）
	e.GET("/index.html", func(c *echo.Context) error {
		return c.Redirect(http.StatusMovedPermanently, "/")
	})
	e.GET(prefix+"/index.html", func(c *echo.Context) error {
		return c.Redirect(http.StatusMovedPermanently, prefix+"/")
	})

	// 其余 GET 一律按静态文件处理。
	// 结尾带 / 的是目录请求：css/ 与 js/ 里没有 index.html，文件服务器会回
	// 目录列表把内嵌资源全列出来，这里直接 404 堵掉。
	// 接口路由（/api/*、/d/*、/static/avatars/:key）是静态或参数路由，
	// 优先级高于这里的 /* 通配，不会被静态文件服务器吃掉。
	//
	// 这里不用 http.FileServer，而是自己走 ServeContent，只为把 ETag/Cache-Control
	// 补上：//go:embed 的文件 ModTime 是**零值**，ServeContent 因此既不发 Last-Modified
	// 也不发 ETag。一个没有任何验证器、也没有新鲜度的 200 响应，浏览器只能按启发式
	// 规则把 home.js 缓存住，而且**没有东西可以回问**——于是「重新编译 + 重启服务」
	// 根本不影响浏览器里那份旧 JS，前端改了也永远看不到，这是最难查的一类问题。
	// 补上按内容算的 ETag 后：内容没变回 304（省掉 66KB 的 home.js），变了立刻生效。
	immutable := !diskMode()
	e.GET("/*", func(c *echo.Context) error {
		req := c.Request()
		if strings.HasSuffix(req.URL.Path, "/") {
			http.NotFound(c.Response(), req)
			return nil
		}
		name := strings.TrimPrefix(req.URL.Path, "/")
		a, err := loadAsset(fsys, name, immutable)
		if err != nil {
			http.NotFound(c.Response(), req)
			return nil
		}
		h := c.Response().Header()
		h.Set("ETag", a.etag)
		h.Set("Cache-Control", "no-cache")
		// ServeContent 认响应头里已有的 ETag：If-None-Match 命中就回 304，
		// Range 请求照旧支持（播放器拖动进度条靠它）。
		http.ServeContent(c.Response(), req, path.Base(name), time.Time{}, bytes.NewReader(a.body))
		return nil
	})
	return nil
}
