package middlewares

import (
	"github.com/azhai/mocca/helpers"
	web "github.com/azhai/mocca/lib/web"
	"github.com/azhai/mocca/models"
)

// AdminMiddleware 要求登录**且**是管理员：先校验令牌，再查库确认角色。
// 角色不放进令牌，避免改权限后旧令牌仍带着旧角色。
func AdminMiddleware(secret string) web.MiddlewareFunc {
	return func(next web.HandlerFunc) web.HandlerFunc {
		return func(c *web.Context) error {
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
