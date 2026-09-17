package handlers

import (
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// requireFolderPassword 校验目录密码；未受保护则直接放行。
//
// 注意入参 secret 是客户端算好的静态哈希（与登录同一套约定），
// 服务端只用 bcrypt 比对，永远不会看到明文。
func requireFolderPassword(reqPath, secret string) error {
	dir, hash, protected, err := models.LookupFolderPassword(reqPath)
	if err != nil {
		return errors.New("校验目录密码失败")
	}
	if !protected {
		return nil
	}
	if models.VerifyFolderPassword(hash, secret) {
		return nil
	}
	return errors.Errorf("目录 %s 需要密码", dir)
}

// SetFolderPassword 设置或清除目录密码（管理员）。password 为空即清除。
//
// 只需给父目录设一次，整棵子树都受保护（见 models.LookupFolderPassword）。
func SetFolderPassword(c *echo.Context) error {
	var req struct {
		Path     string `json:"path"`
		Password string `json:"password"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}

	if req.Password == "" {
		if err := models.ClearFolderPassword(req.Path); err != nil {
			return helpers.Fail(c, helpers.CodeInternal, "清除目录密码失败")
		}
		return helpers.OK(c, map[string]any{"path": req.Path, "protected": false})
	}

	if err := models.SetFolderPassword(req.Path, req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "设置目录密码失败")
	}
	return helpers.OK(c, map[string]any{"path": req.Path, "protected": true})
}

// FolderPasswordStatus 查询某路径是否受保护（不泄露哈希）。
func FolderPasswordStatus(c *echo.Context) error {
	p := c.QueryParam("path")
	if p == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	dir, _, protected, err := models.LookupFolderPassword(p)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "查询失败")
	}
	return helpers.OK(c, map[string]any{"protected": protected, "protected_dir": dir})
}
