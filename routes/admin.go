package routes

import (
	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/middlewares"
	"github.com/labstack/echo/v5"
)

// SetupAdminRoutes 注册管理类接口（管理员令牌才能用）。
//
// **单独一个函数、不带构建标签**，两种构建都参与编译：
//   - 完整版由 SetupAPIRoutes 在 AdminRoutes 为真时调用（见 routes.go）；
//   - 纯 API 版不调用，于是这一整组路由根本不存在（不是"存在但报错"，
//     而是跟其它未命中路径一样走默认 404）；
//   - 测试直接调用它，所以管理接口的处理器在两种构建下都被覆盖到
//     （构建标签的剥离效果另由 routes 包的测试断言，见 routes_test.go）。
//
// 把路由表放在一个函数里而不是按标签拆两份，是为了避免"改了这边忘了那边"：
// 路由清单只有一份，差异只是一个布尔。
func SetupAdminRoutes(e *echo.Echo) {
	secret := config.Cfg.JWTSecret
	api := e.Group(config.APIBaseURL)
	admin := api.Group("", middlewares.AdminMiddleware(secret))

	// 挂载点与元数据维护
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
	admin.POST("/fs/export", handlers.FsExport)   // 数据迁移：把选中视频的 .mocca 数据打包为 tar.gz
	admin.POST("/fs/uncov", handlers.FsUncov)     // 删除封面（与上传封面/FFmpeg 截图相对）
	admin.POST("/fs/patch", handlers.FsPatch)     // 目录级补充截图：给缺封面的视频批量生封面
	// TMDB 刮削：先检索候选，再把选中的那条写进 .mocca 附加信息与封面
	admin.POST("/fs/scrape", handlers.ScrapeSearch)
	admin.POST("/fs/scrape/apply", handlers.ScrapeApply)
	// 目录密码：给父目录设一次即可保护整棵子树
	admin.POST("/folder/password", handlers.SetFolderPassword)
	// 全局选项：后台开关（是否开放注册、游客可浏览、文件监控等）
	admin.GET("/setting/list", handlers.ListSettings)
	admin.POST("/setting/update", handlers.UpdateSetting)
	// 用户管理
	admin.GET("/user/list", handlers.ListUsers)
	admin.POST("/user/create", handlers.CreateUserByAdmin)
	admin.POST("/user/update", handlers.UpdateUserByAdmin)
	admin.POST("/user/delete", handlers.DeleteUserByAdmin)
}
