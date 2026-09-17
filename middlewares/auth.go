package middlewares

import (
	"strings"

	"github.com/azhai/mocca/helpers"
	"github.com/labstack/echo/v5"
)

// UserIDKey 上下文中存放用户 ID 的键。
const UserIDKey = "user_id"

// tokenFrom 取令牌：客户端把 JWT 直接放进 Authorization 头，
// 兼容带 Bearer 前缀的写法；播放器还会用 ?token= 传，见 TokenFrom。
func tokenFrom(c *echo.Context) string {
	return TokenFrom(c.Request().Header.Get("Authorization"))
}

// TokenFrom 从原始头值里剥出令牌。
func TokenFrom(header string) string {
	if header == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

// AuthMiddleware 要求登录，未登录或令牌失效返回 401。
func AuthMiddleware(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			tok := tokenFrom(c)
			if tok == "" {
				return helpers.Fail(c, helpers.CodeUnauthorized, "未登录")
			}
			claims, err := helpers.ParseToken(secret, tok)
			if err != nil {
				return helpers.Fail(c, helpers.CodeUnauthorized, "登录已失效")
			}
			c.Set(UserIDKey, claims.UserID)
			return next(c)
		}
	}
}

// OptionalAuth 有令牌就解析，没有也放行（列目录按游客权限处理）。
func OptionalAuth(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if tok := tokenFrom(c); tok != "" {
				if claims, err := helpers.ParseToken(secret, tok); err == nil {
					c.Set(UserIDKey, claims.UserID)
				}
			}
			return next(c)
		}
	}
}

// StreamAuth 供播放器取流（`/d/*path`）。令牌可放请求头或查询串 ?token=
// ——播放器不一定能自定义请求头，但都会原样带查询串。
//
// 三种情况：
//   - 没带令牌：按 allowGuest() 决定放行还是拒绝。**播放必须与浏览用同一个开关**：
//     浏览不需要登录、播放却要求登录的话，游客点任何视频/音频都只会报错。
//   - 带了但无效/过期：直接 401，不降级成游客 —— 降级会让「登录失效」表现为
//     静默变成游客，问题更难查。
//   - 带了且有效：放行并写入用户 ID。
//
// allowGuest 传函数而非布尔值：允许游客是运行期可改的设置，
// 在装配路由时求值会把状态焊死在启动那一刻。
//
// 失败一律用 FailStatus 回真正的 HTTP 状态码：本接口的响应体是要被播放器
// 当作字节流解析的，回 HTTP 200 + JSON 会让播放器把错误信封当媒体数据去解码，
// 报出来的错与真实原因毫无关系。
func StreamAuth(secret string, allowGuest func() bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			tok := tokenFrom(c)
			if tok == "" {
				tok = c.QueryParam("token")
			}
			if tok == "" {
				if allowGuest != nil && allowGuest() {
					return next(c)
				}
				return helpers.FailStatus(c, helpers.CodeUnauthorized, "未登录")
			}
			claims, err := helpers.ParseToken(secret, tok)
			if err != nil {
				return helpers.FailStatus(c, helpers.CodeUnauthorized, "登录已失效")
			}
			c.Set(UserIDKey, claims.UserID)
			return next(c)
		}
	}
}

// UserID 取当前用户 ID，未登录返回 0。
func UserID(c *echo.Context) uint {
	v, _ := c.Get(UserIDKey).(uint)
	return v
}
