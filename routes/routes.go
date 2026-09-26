package routes

import (
	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// SetupAPIRoutes 注册全部接口。
//
// 分组即鉴权策略，一眼能看出每个接口的权限要求：
// admin（管理员）> authed（需登录）> optional（登录可选）> api（公开）。
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

	// 管理员：挂载点与元数据维护
	admin := api.Group("", middlewares.AdminMiddleware(secret))
	admin.GET("/storage/list", handlers.ListStorage)
	admin.POST("/storage/create", handlers.CreateStorage)
	admin.POST("/storage/update", handlers.UpdateStorage)
	admin.POST("/storage/delete", handlers.DeleteStorage)
	admin.GET("/storage/scan", handlers.ScanMounts) // 自动发现外接设备根目录（管理员）
	// 上传：批量与单文件各一个入口，前端按文件并发调用单文件接口拿独立进度
	admin.POST("/fs/upload", handlers.Upload)
	admin.POST("/fs/put", handlers.UploadOne)
	admin.POST("/fs/remove", handlers.FsRemove)
	admin.POST("/fs/rename", handlers.FsRename)
	admin.POST("/fs/move", handlers.FsMove)
	admin.POST("/fs/edit", handlers.FsEdit)       // 媒体条目编辑：改名 + 附加信息
	admin.POST("/fs/cov", handlers.FsCov)         // 上传替换音/视频封面
	admin.POST("/fs/shot", handlers.FsShot)       // FFmpeg 指定秒数截图作视频封面
	admin.POST("/fs/hls", handlers.FsHLS)         // 切分旁路 HLS：.hls/<文件名>/（只切不转）
	admin.POST("/fs/reindex", handlers.FsReindex) // 按增量重建索引：比 size+modified，变了才重算 sha1
	admin.POST("/fs/uncov", handlers.FsUncov)     // 删除封面（与上传封面/FFmpeg 截图相对）
	admin.POST("/fs/patch", handlers.FsPatch)     // 目录级补充截图：给缺封面的视频批量生封面
	// TMDB 刮削：先检索候选，再把选中的那条写进 .mocca 附加信息与封面
	admin.POST("/fs/scrape", handlers.ScrapeSearch)
	admin.POST("/fs/scrape/apply", handlers.ScrapeApply)
	// 目录密码：给父目录设一次即可保护整棵子树
	admin.POST("/folder/password", handlers.SetFolderPassword)
	api.GET("/folder/status", handlers.FolderPasswordStatus)
	// 全局选项：后台开关（是否开放注册、游客可浏览、文件监控等）
	admin.GET("/setting/list", handlers.ListSettings)
	admin.POST("/setting/update", handlers.UpdateSetting)
	// 用户管理
	admin.GET("/user/list", handlers.ListUsers)
	admin.POST("/user/create", handlers.CreateUserByAdmin)
	admin.POST("/user/update", handlers.UpdateUserByAdmin)
	admin.POST("/user/delete", handlers.DeleteUserByAdmin)

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
}
