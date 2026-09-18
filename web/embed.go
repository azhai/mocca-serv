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
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"

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
			c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
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

	// 其余 GET 一律按静态文件处理。
	// 结尾带 / 的是目录请求：css/ 与 js/ 里没有 index.html，FileServer 会回
	// 目录列表把内嵌资源全列出来，这里直接 404 堵掉。
	// 接口路由（/api/*、/d/*、/static/avatars/:key）是静态或参数路由，
	// 优先级高于这里的 /* 通配，不会被静态文件服务器吃掉。
	server := http.FileServer(http.FS(fsys))
	e.GET("/*", func(c *echo.Context) error {
		if strings.HasSuffix(c.Request().URL.Path, "/") {
			http.NotFound(c.Response(), c.Request())
			return nil
		}
		server.ServeHTTP(c.Response(), c.Request())
		return nil
	})
	return nil
}
