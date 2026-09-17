//go:build noweb

package web

import (
	"io/fs"

	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// Embedded 纯 API 构建里没有后台资源，恒为 false（另一取值见 embed.go）。
const Embedded = false

// ErrNotEmbedded 纯 API 构建里拿不到后台资源。
var ErrNotEmbedded = errors.New("this build has no admin UI: rebuild without -tags noweb")

// FS 没有内容可给：public/ 一整个目录都没参与编译。
func FS() (fs.FS, error) { return nil, ErrNotEmbedded }

// Register 是空操作：不注册任何 /admin 路由，于是 /admin/* 与其它未命中
// 路径一样落到默认 404，不会出现「路由在、页面白屏」这种半死状态。
//
// 签名与 embed.go 里的 Register 保持一致，main.go 才能无条件调用它。
func Register(_ *echo.Echo, _ string) error { return nil }
