package routes

import (
	"strings"
	"testing"

	"github.com/azhai/mocca/config"
	"github.com/labstack/echo/v5"
)

// requiredRoutes APP（与浏览器浏览端）要用的接口，**两种构建都必须有**。
// 列在这里的作用是防止「顺手收窄」把必需接口一起删掉。
var requiredRoutes = []string{
	"GET /ping",
	"POST /api/auth/login/hash",
	"GET /api/init/status",
	"POST /api/fs/list",
	"POST /api/fs/get",
	"POST /api/fs/info",
	"GET /api/danmaku",
	"POST /api/danmaku",
	"GET /api/danmaku/stream",
	"GET /api/comments",
	"POST /api/comments",
	"GET /api/folder/status",
	"GET /api/me",
	"GET /api/favorites",
	"POST /api/favorites",
	"GET /meta/poster",
	"GET /d/*path",
}

// adminRoutes 管理类接口：只在完整版注册，纯 API 版（-tags noweb）整组不挂。
var adminRoutes = []string{
	"POST /api/storage/create",
	"GET /api/storage/list",
	"POST /api/fs/upload",
	"POST /api/fs/edit",
	"POST /api/fs/cov",
	"POST /api/fs/shot",
	"POST /api/fs/patch",
	"POST /api/fs/scrape",
	"POST /api/folder/password",
	"POST /api/setting/update",
	"POST /api/user/create",
}

// TestRouteTableMatchesBuild 路由表必须与构建一致：
// 必需接口两种构建都在；管理接口只在 AdminRoutes 为真的构建里（完整版）。
//
// 这比「发个请求看状态码」更直接：断言的是路由**注册表**本身，
// 不依赖数据库，也不会被中间件的鉴权响应混淆。
func TestRouteTableMatchesBuild(t *testing.T) {
	config.Cfg = &config.Config{JWTSecret: "unit-test-secret"}
	e := echo.New()
	SetupAPIRoutes(e)

	have := map[string]bool{}
	for _, r := range e.Router().Routes() {
		have[r.Method+" "+strings.TrimSuffix(r.Path, "/")] = true
	}
	has := func(methodPath string) bool {
		m, p, _ := strings.Cut(methodPath, " ")
		if have[methodPath] {
			return true
		}
		// 通配路由在登记表里可能写作 *path 或 *
		return have[m+" "+strings.TrimSuffix(p, "*path")+"*"]
	}

	for _, key := range requiredRoutes {
		if !has(key) {
			t.Errorf("%s 是必需接口，任何构建都应注册", key)
		}
	}
	for _, key := range adminRoutes {
		if has(key) != AdminRoutes {
			if AdminRoutes {
				t.Errorf("%s 在完整版里应注册", key)
			} else {
				t.Errorf("%s 在纯 API 构建（-tags noweb）里不应注册", key)
			}
		}
	}
}
