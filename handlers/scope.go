package handlers

import (
	"path"
	"strings"

	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// SettingAllowGuest 游客（未登录）能否浏览。默认允许，便于手机端先看再登录。
const SettingAllowGuest = "allow_guest"

// scopePath 把请求路径收敛到用户自己的目录。
//
//   - 未登录：按 allow_guest 开关决定放行或拒绝；
//   - 普通用户：若配了 base_path，则强制前缀，用户只能看到自己目录下的东西；
//   - 管理员：不受限。
func scopePath(c *echo.Context, reqPath string) (string, error) {
	uid := middlewares.UserID(c)
	if uid == 0 {
		if !models.SettingBool(SettingAllowGuest, true) {
			return "", errors.New("未登录不可浏览")
		}
		return reqPath, nil
	}

	u, err := models.GetUserByID(uid)
	if err != nil {
		return "", errors.New("账号不存在")
	}
	if u.IsAdmin() || u.BasePath == "" {
		return reqPath, nil
	}

	base := path.Clean("/" + strings.TrimSpace(u.BasePath))
	if base == "/" {
		return reqPath, nil
	}
	joined := path.Join(base, reqPath)
	// path.Join 会把 ".." 归一化掉：base=/alice 遇上 /../secret 会拼出 /secret，
	// 于是普通用户能寻址到 base_path 之外。拼完必须再确认一次前缀，越界直接拒。
	if joined != base && !strings.HasPrefix(joined, base+"/") {
		return "", errors.New("路径越出专属目录")
	}
	return joined, nil
}
