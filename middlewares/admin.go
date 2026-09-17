package middlewares

import (
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// AdminMiddleware 要求登录**且**是管理员：先校验令牌，再查库确认角色。
// 角色不放进令牌，避免改权限后旧令牌仍带着旧角色。
func AdminMiddleware(secret string) echo.MiddlewareFunc {
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
			u, err := models.GetUserByID(claims.UserID)
			if err != nil {
				return helpers.Fail(c, helpers.CodeUnauthorized, "账号不存在")
			}
			if !u.IsAdmin() {
				return helpers.Fail(c, helpers.CodeForbidden, "需要管理员权限")
			}
			c.Set(UserIDKey, u.ID)
			return next(c)
		}
	}
}
