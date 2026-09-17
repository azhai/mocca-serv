package helpers

import "github.com/labstack/echo/v5"

// 业务错误码。
//
// 关键约定：HTTP 状态码一律 200，成败只看响应体的 code ——
// 客户端 ApiClient 就是按这个约定解析信封的（validateStatus 全放行）。
const (
	CodeOK           = 200
	CodeBadRequest   = 400
	CodeUnauthorized = 401
	CodeForbidden    = 403
	CodeNotFound     = 404
	CodeInternal     = 500
)

// Resp 统一信封。
type Resp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// OK 成功响应；不传 data 时为 null。
func OK(c *echo.Context, data ...any) error {
	var d any
	if len(data) > 0 {
		d = data[0]
	}
	return c.JSON(200, Resp{Code: CodeOK, Message: "success", Data: d})
}

// Fail 失败响应，HTTP 仍是 200。
func Fail(c *echo.Context, code int, msg string) error {
	return c.JSON(200, Resp{Code: code, Message: msg})
}

// FailStatus 失败响应，且 HTTP 状态码与业务码一致。
//
// 专供取流（`/d/*path`）这类「响应体就是要被当成字节流解析」的接口：
// 播放器不看信封、只认状态码。失败若回 HTTP 200，播放器会把那段 JSON
// 当成媒体数据去解码，最终报出来的错（格式不支持之类）与真实原因无关，
// 排查时会被彻底带偏。
func FailStatus(c *echo.Context, code int, msg string) error {
	return c.JSON(code, Resp{Code: code, Message: msg})
}
