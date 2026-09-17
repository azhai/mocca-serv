package handlers

import (
	"net/http"
	"time"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// SettingAllowRegister settings 表里控制「是否开放注册」的键。
// 存在库里而不是常量，是为了能在后台随时开关、无需重新编译。
const SettingAllowRegister = "allow_register"

// LoginReq 注册与登录共用：password 是客户端算好的静态哈希
// sha256(明文 + "-" + "https://github.com/alist-org/alist")，
// 服务端还会再过一次 bcrypt，库里永远只有不可逆哈希。
type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// requireStaticHash 校验客户端提交的口令是「静态哈希」而不是明文。
//
// 必须挡：models.SetPassword 只做 bcrypt，而登录是拿 staticHash(明文) 去比对的。
// 明文一旦进来就会存成 bcrypt(明文)，该账号**永远登不进去、而且不报任何错** ——
// 后台的用户编辑表单真这么干过（2026-09-17「登录不了」事故收尾时发现）。
// 把它变成响亮的 400，比让它静默写坏账号好得多。
func requireStaticHash(secret string) error {
	if !models.IsStaticHashText(secret) {
		return errors.New("口令格式不正确：客户端须先做静态哈希再提交")
	}
	return nil
}

// MeResp 当前用户信息。头像给的是预设 key，前端拼 /static/avatars/<key>.png。
type MeResp struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
	Role     int    `json:"role"`
	BasePath string `json:"base_path"`
}

// InitStatus 初始化状态：前端据此决定是引导建管理员还是走登录。
type InitStatus struct {
	Initialized   bool `json:"initialized"`    // 是否已有账号
	AllowRegister bool `json:"allow_register"` // 是否开放注册
}

// Register 注册新账号。
//
// 首启引导：库中还没有任何账号时，第一个注册者直接成为管理员。
// 否则新装的服务无人能触达管理员接口，连存储都配不了。
func Register(c *echo.Context) error {
	var req LoginReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}

	count, err := models.CountUsers()
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "初始化检查失败")
	}
	firstUser := count == 0

	// 已有账号且未开放注册则拒绝；首个账号始终放行（否则会锁死）
	if !firstUser && !models.SettingBool(SettingAllowRegister, config.Cfg.AllowRegister) {
		return helpers.Fail(c, helpers.CodeForbidden, "服务端未开放注册")
	}

	name := models.NormalizeUsername(req.Username)
	if name == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "用户名不能为空")
	}
	if _, err = models.GetUserByName(name); err == nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "用户名已存在")
	}

	u := &models.User{Username: name, Role: models.RoleGeneral}
	if firstUser {
		u.Role = models.RoleAdmin
	}
	if err = requireStaticHash(req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	if err = u.SetPassword(req.Password); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	if err = models.CreateUser(u); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "创建账号失败")
	}
	return helpers.OK(c, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"role":     u.Role,
		"is_admin": u.IsAdmin(),
	})
}

// InitStatusHandler 公开的初始化状态。
func InitStatusHandler(c *echo.Context) error {
	count, err := models.CountUsers()
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "初始化检查失败")
	}
	return helpers.OK(c, InitStatus{
		Initialized:   count > 0,
		AllowRegister: models.SettingBool(SettingAllowRegister, true),
	})
}

// LoginHash 静态哈希登录，返回 JWT。
func LoginHash(c *echo.Context) error {
	var req LoginReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	u, err := models.GetUserByName(models.NormalizeUsername(req.Username))
	if err != nil {
		// 账号不存在与密码错误返回同一句，避免暴露用户名是否存在
		return helpers.Fail(c, helpers.CodeUnauthorized, "用户名或密码错误")
	}
	if u.Disabled {
		return helpers.Fail(c, helpers.CodeUnauthorized, "账号已被禁用")
	}
	if !u.CheckPassword(req.Password) {
		return helpers.Fail(c, helpers.CodeUnauthorized, "用户名或密码错误")
	}
	ttl := time.Duration(config.Cfg.TokenExpiresIn) * time.Hour
	token, err := helpers.SignToken(config.Cfg.JWTSecret, u.ID, ttl)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "签发令牌失败")
	}
	_ = models.TouchLogin(u) // 登录时间记不成功也不影响登录
	return helpers.OK(c, map[string]any{"token": token})
}

// Me 当前登录用户。
func Me(c *echo.Context) error {
	u, err := models.GetUserByID(middlewares.UserID(c))
	if err != nil {
		return helpers.Fail(c, helpers.CodeUnauthorized, "账号不存在")
	}
	return helpers.OK(c, MeResp{
		ID:       u.ID,
		Username: u.Username,
		Avatar:   u.AvatarOrDefault(),
		Role:     u.Role,
		BasePath: u.BasePath,
	})
}

// Ping 连通性探测，返回纯文本 pong（不是 JSON 信封）。
func Ping(c *echo.Context) error {
	return c.String(http.StatusOK, "pong")
}
