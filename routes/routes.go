package routes

import (
	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// SetupAPIRoutes 注册接口。
//
// 分组即鉴权策略，一眼能看出每个接口的权限要求：
// admin（管理员）> authed（需登录）> optional（登录可选）> api（公开）。
//
// 这里只注册**两种构建都要**的那部分（APP 与浏览器浏览端共用）。
// 管理类接口（存储维护、上传改名、媒体编辑、封面制作、刮削、全局设置、用户管理）
// 单独放在 SetupAdminRoutes 里，由 AdminRoutes 决定要不要挂：
// 完整版挂全套，纯 API 版（-tags noweb）一个不挂 —— 见 admin.go 与 build_*.go。
func SetupAPIRoutes(e *echo.Echo) {
	secret := config.Cfg.JWTSecret

	// 探活：根与 /api 下各挂一个，兼容反向代理剥前缀的部署
	e.GET("/ping", handlers.Ping)

	api := e.Group(config.APIBaseURL)
	api.Any("/ping", handlers.Ping)

	// 公开：初始化状态、注册、登录、头像
	api.GET("/init/status", handlers.InitStatusHandler)
	api.POST("/auth/register", handlers.Register)
	api.POST("/auth/login/hash", handlers.LoginHash)
	e.GET("/static/avatars/:key", handlers.Avatar)

	// 只读：评论、弹幕（浏览态也能看）
	api.GET("/comments", handlers.ListComments)
	api.GET("/danmaku", handlers.ListDanmaku)

	// 需登录
	authed := api.Group("", middlewares.AuthMiddleware(secret))
	authed.GET("/me", handlers.Me)
	authed.POST("/me/update", handlers.UpdateMe)
	authed.GET("/favorites", handlers.ListFavorites)
	authed.POST("/favorites", handlers.AddFavorite)
	authed.DELETE("/favorites", handlers.RemoveFavorite)
	authed.POST("/comments", handlers.AddComment)
	authed.DELETE("/comments", handlers.DeleteComment)
	authed.POST("/danmaku", handlers.AddDanmaku)

	// 目录密码状态：客户端据此决定要不要弹口令框（只读，两种构建都要）
	api.GET("/folder/status", handlers.FolderPasswordStatus)

	// 登录可选：未登录按游客权限列目录
	optional := api.Group("", middlewares.OptionalAuth(secret))
	optional.POST("/fs/list", handlers.FsList)
	optional.POST("/fs/get", handlers.FsGet)
	optional.POST("/fs/info", handlers.FsInfo) // 悬浮层详情：sha1/海报/简介/元数据
	// 弹幕实时推送（SSE）。放这组是因为 EventSource 只能把令牌塞在查询串里，
	// 有就解析、没有按游客处理 —— 与"浏览态也能看弹幕"的只读口径一致。
	optional.GET("/danmaku/stream", handlers.DanmakuStream)

	// 海报图：<img> 加载，令牌走查询串，鉴权与取流同款（StreamAuth）
	e.GET("/meta/poster", handlers.MetaPoster, middlewares.StreamAuth(secret, func() bool {
		return models.SettingBool(handlers.SettingAllowGuest, true)
	}))

	// 取流：令牌可放请求头或查询串；没带令牌时按「允许游客」开关与浏览保持一致
	e.GET("/d/*path", handlers.Download, middlewares.StreamAuth(secret, func() bool {
		return models.SettingBool(handlers.SettingAllowGuest, true)
	}))

	// 管理类接口：纯 API 构建（-tags noweb）里不注册，整组不存在
	if AdminRoutes {
		SetupAdminRoutes(e)
	}
}
