# lib/web —— 仓库自用的最小 HTTP 框架

路由 + 中间件 + 响应封装，替代 `labstack/echo`（见 ADR 0007）。形状刻意与 echo v5 对齐，
调用点只是换包名；没有第三方 HTTP 依赖，`-tags noweb` 的页面剥离、`http.Server` 的超时与
优雅关闭都握在自己手里。

## 快速上手

`Echo` 只是一个 `http.Handler`（**没有**内置 `Start`/`Shutdown`），交给标准库的 `http.Server`：

```go
e := web.New()
e.Use(middlewares.RecoverLog())     // 全局中间件：洋葱最外层，先注册的包住后面所有
e.GET("/api/ping", ping)            // 实例层注册 = 空前缀组
admin := e.Group("/admin", requireAdmin) // Group：前缀 + 中间件继承
admin.POST("/media/:sha1/reindex", reindex)

srv := &http.Server{Handler: e, ReadTimeout: 30 * time.Second, WriteTimeout: 0}
```

## 路由

- 段树匹配，同层优先级：**静态 > `:param` > `*wild`**。
  `GET /api/:id` 命中 `/api/42`，`Param("id")=="42"`；`GET /files/*rest` 用 `Param("rest")` 取剩余路径。
- **尾斜杠是独立路由**（与 echo 默认一致）：`/admin` 与 `/admin/` 不塌成同一节点，重定向要自己显式注册。
- 方法注册：`GET / POST / PUT / DELETE / Any`（`Any` = 不限方法）。
- 未命中任何路由 → `Echo.NotFound`（默认回 404）。

## 中间件（洋葱）

```go
type MiddlewareFunc func(web.HandlerFunc) web.HandlerFunc
```

全局 `Use` 在外、路由级 `Use`/注册时的 `mw...` 在内。先注册的包住后注册的，所以 panic 兜底
中间件必须是第一个 `Use`。

## Context（处理器签名 `func(*web.Context) error`）

本仓真实用到的 12 个方法：

| 类别 | 方法 |
| --- | --- |
| 入参 | `Bind(i any)`（JSON body）、`QueryParam`、`Param`、`FormValue`、`MultipartForm` |
| 出参 | `JSON(code, v)`、`String(code, format, args...)`、`Redirect(code, location)` |
| 底层 | `Request() *http.Request`、`Response() http.ResponseWriter`（SSE/流媒体直接写、`Flush` 自己断言 `http.Flusher`） |
| 上下文值 | `Set(key, v)` / `Get(key)`（每请求作用域） |

## 错误约定

- **业务失败一律回 HTTP 200 + 信封体**（`{code, msg, data}`，见 `helpers.Fail`）——这是后台的固定契约。
- 处理器返回的 non-nil `error` 走 `Echo.ErrorHandler`，**不回** echo 那种 `*echo.HTTPError`（本仓一处没用）。
- 默认 `ErrorHandler` 只在「响应还没开始写」时补一个 500：流媒体写到一半 / SSE 已发响应头时不再写任何东西。

## 与页面构建标签的关系

`-tags noweb` 换的是 **`web/`（前端嵌入包）**，不是本库：`web/embed.go` 编译进后台页面，
`web/noweb.go`（`//go:build noweb`）把 `web.Register` 变成空操作。`lib/web` 本身永远参与编译。
