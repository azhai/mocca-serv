package middlewares

import (
	"log"
	"time"

	"github.com/labstack/echo/v5"
)

// RequestLogger 记录每个请求的方法、路径、耗时与错误。
//
// 只看启动日志没法排查线上问题，请求日志是排障的第一手材料；
// 慢请求（>= slowThreshold）额外标注，便于发现卡顿的存储。
//
// 注：这里不记 HTTP 状态码 —— echo v5 的 c.Response() 是 http.ResponseWriter，
// 读不到已写入的状态码，要记就得自己包一层 ResponseWriter，不值得。
// 业务成败本来也在响应体的 code 字段里。
func RequestLogger(slowThreshold time.Duration) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			start := time.Now()
			err := next(c)
			cost := time.Since(start)
			req := c.Request()

			switch {
			case err != nil:
				log.Printf("%s %s 错误=%v 耗时=%s", req.Method, req.URL.Path, err, cost)
			case cost >= slowThreshold:
				log.Printf("%s %s 慢请求 耗时=%s", req.Method, req.URL.Path, cost)
			default:
				log.Printf("%s %s 耗时=%s", req.Method, req.URL.Path, cost)
			}
			return err
		}
	}
}
