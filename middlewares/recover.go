package middlewares

import (
	"strings"

	"github.com/azhai/mocca/helpers"
	"github.com/labstack/echo/v5"
)

// RecoverLog 捕获请求链里的 panic：把 panic 值与调用栈写进错误日志，
// 再回一句「服务内部错误」，而不是让 panic 打穿到 net/http。
//
// 一个 handler 崩了不该带走整个进程（正在播放的流会全断），但更不能静默：
// 没有调用栈，线上只能看到「服务莫名重启」，排查只能靠猜。
//
// 例外：`/d/*` 取流接口按真实 HTTP 500 回——播放器不看信封、只认状态码
// （理由同 helpers.FailStatus）。
func RecoverLog() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			defer func() {
				if r := recover(); r != nil {
					req := c.Request()
					helpers.Panicf("http "+req.Method+" "+req.URL.Path, r)
					err = panicFail(c)
				}
			}()
			return next(c)
		}
	}
}

// panicFail 按接口类型选失败形态：取流回真实状态码，其余回统一信封。
func panicFail(c *echo.Context) error {
	const msg = "服务内部错误"
	if req := c.Request(); req != nil && strings.HasPrefix(req.URL.Path, "/d/") {
		return helpers.FailStatus(c, helpers.CodeInternal, msg)
	}
	return helpers.Fail(c, helpers.CodeInternal, msg)
}
