package middlewares

import (
	"net/http"
	"strconv"

	"github.com/azhai/mocca/helpers"
	"github.com/labstack/echo/v5"
)

// DefaultMaxBody 单请求体积上限（1GB）。
// 上传接口必须限流，否则一个超大请求就能把磁盘写满、把连接占死。
const DefaultMaxBody int64 = 1 << 30

// BodyLimit 限制请求体大小：超限直接 413，并在读取时二次兜底
// （有些客户端不报 Content-Length，只靠头部判断会被绕过）。
func BodyLimit(max int64) echo.MiddlewareFunc {
	if max <= 0 {
		max = DefaultMaxBody
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			if req.ContentLength > max {
				return helpers.Fail(c, helpers.CodeBadRequest,
					"请求体过大，上限 "+humanSize(max))
			}
			req.Body = http.MaxBytesReader(c.Response(), req.Body, max)
			return next(c)
		}
	}
}

// humanSize 把字节数转成便于阅读的 MB/GB。
func humanSize(n int64) string {
	const mb = 1 << 20
	if n >= 1<<30 {
		return strconv.FormatInt(n>>30, 10) + "GB"
	}
	return strconv.FormatInt(n/mb, 10) + "MB"
}
