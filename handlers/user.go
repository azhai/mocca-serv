package handlers

import (
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// UserReq 用户管理请求。Password 非空才改密，避免编辑资料时误清空密码。
type UserReq struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Role     int    `json:"role"`
	BasePath string `json:"base_path"`
	Avatar   string `json:"avatar"`
	Disabled bool   `json:"disabled"`
}

// ListUsers 账号列表（不含任何密码字段，User.PasswordHash 是 `json:"-"`）。
func ListUsers(c *echo.Context) error {
	all, err := models.ListUsers()
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取用户失败")
	}
	return helpers.OK(c, all)
}

// CreateUserByAdmin 管理员建号。
func CreateUserByAdmin(c *echo.Context) error {
	var req UserReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	name := models.NormalizeUsername(req.Username)
	if name == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "用户名不能为空")
	}
	if _, err := models.GetUserByName(name); err == nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "用户名已存在")
	}

	u := &models.User{
		Username: name,
		Role:     req.Role,
		BasePath: req.BasePath,
		Disabled: req.Disabled,
	}
	if err := requireStaticHash(req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	if err := u.SetPassword(req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	if req.Avatar != "" {
		if err := u.SetAvatar(req.Avatar); err != nil {
			return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
		}
	}
	if err := models.CreateUser(u); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "创建账号失败")
	}
	return helpers.OK(c, u)
}

// UpdateUserByAdmin 管理员改号。
func UpdateUserByAdmin(c *echo.Context) error {
	var req UserReq
	if err := c.Bind(&req); err != nil || req.ID == 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	u, err := models.GetUserByID(req.ID)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "账号不存在")
	}

	// 不允许把最后一个管理员降级，否则没人能再进管理后台
	if u.IsAdmin() && req.Role != models.RoleAdmin {
		if n, err := models.CountAdmins(); err == nil && n <= 1 {
			return helpers.Fail(c, helpers.CodeBadRequest, "不能降级最后一个管理员")
		}
	}

	if req.Username != "" {
		name := models.NormalizeUsername(req.Username)
		if other, err := models.GetUserByName(name); err == nil && other.ID != u.ID {
			return helpers.Fail(c, helpers.CodeBadRequest, "用户名已被占用")
		}
		u.Username = name
	}
	u.Role = req.Role
	u.BasePath = req.BasePath
	u.Disabled = req.Disabled
	if req.Avatar != "" {
		if err := u.SetAvatar(req.Avatar); err != nil {
			return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
		}
	}
	if req.Password != "" {
		if err := requireStaticHash(req.Password); err != nil {
			return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
		}
		if err := u.SetPassword(req.Password); err != nil {
			return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
		}
	}
	if err = models.UpdateUser(u); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "更新账号失败")
	}
	return helpers.OK(c, u)
}

// DeleteUserByAdmin 删号，同样保护最后一个管理员。
func DeleteUserByAdmin(c *echo.Context) error {
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.Bind(&req); err != nil || req.ID == 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	u, err := models.GetUserByID(req.ID)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "账号不存在")
	}
	if u.IsAdmin() {
		if n, err := models.CountAdmins(); err == nil && n <= 1 {
			return helpers.Fail(c, helpers.CodeBadRequest, "不能删除最后一个管理员")
		}
	}
	if err = models.DeleteUser(req.ID); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "删除账号失败")
	}
	return helpers.OK(c, nil)
}

// UpdateMe 本人修改资料：只能改头像与密码，改不了角色与目录。
func UpdateMe(c *echo.Context) error {
	var req struct {
		Avatar      string `json:"avatar"`
		Password    string `json:"password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	u, err := models.GetUserByID(middlewares.UserID(c))
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, "账号不存在")
	}

	if req.NewPassword != "" {
		// 改密必须校验原密码，防止令牌被盗后直接改密
		if !u.CheckPassword(req.Password) {
			return helpers.Fail(c, helpers.CodeForbidden, "原密码不正确")
		}
		if err = requireStaticHash(req.NewPassword); err != nil {
			return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
		}
		if err = u.SetPassword(req.NewPassword); err != nil {
			return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
		}
	}
	if req.Avatar != "" {
		if err = u.SetAvatar(req.Avatar); err != nil {
			return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
		}
	}
	if err = models.UpdateUser(u); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "更新失败")
	}
	return helpers.OK(c, MeResp{
		ID:       u.ID,
		Username: u.Username,
		Avatar:   u.AvatarOrDefault(),
		Role:     u.Role,
		BasePath: u.BasePath,
	})
}
