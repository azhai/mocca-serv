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

// Register 把后台挂到 prefix 下。二进制自带资源，运行时不依赖任何外部目录。
func Register(e *echo.Echo, prefix string) error {
	sub, err := FS()
	if err != nil {
		return err
	}
	server := http.StripPrefix(prefix+"/", http.FileServer(http.FS(sub)))
	handler := func(c *echo.Context) error {
		server.ServeHTTP(c.Response(), c.Request())
		return nil
	}
	// 不带尾斜杠时 StripPrefix 会直接 404（路径不以 prefix+"/" 开头），
	// 所以这里重定向到带斜杠的规范入口。
	e.GET(prefix, func(c *echo.Context) error {
		return c.Redirect(http.StatusFound, prefix+"/")
	})
	e.GET(prefix+"/*", handler)
	return nil
}
