package models

import (
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"
)

// 角色取值必须与 APP 端 `Session.role` 对齐：
// 0=普通用户 / 1=游客 / 2=管理员（APP 里 `isAdmin => role == 2`）。
const (
	RoleGeneral = iota
	RoleGuest
	RoleAdmin
)

// AvatarPresets 服务端预设头像。用户只能从中选一个，不支持上传自定义图片：
// 既省掉存储与鉴黄成本，也避免把用户文件当头像带来的越权读取风险。
// 值为文件名，前端拼 /static/avatars/<key>.png。
var AvatarPresets = []string{
	"01", "02", "03", "04", "05", "06",
	"07", "08", "09", "10", "11", "12",
}

// DefaultAvatar 未选择时的默认头像。
const DefaultAvatar = "01"

// IsValidAvatar 判断头像是否为预设之一。
func IsValidAvatar(key string) bool {
	for _, p := range AvatarPresets {
		if p == key {
			return true
		}
	}
	return false
}

// User 账号表。
//
// 安全约定（不可妥协）：
//   - 库里只有 PasswordHash —— bcrypt 不可逆哈希，带随机盐；
//   - 不存明文密码，不存可逆加密结果，不存独立的 Salt 列（bcrypt 自带盐）；
//   - 明文/客户端静态哈希只在单次请求的内存里存在，落库前必过 bcrypt。
type User struct {
	ID           uint      `json:"id" goe:"pk"`
	Username     string    `json:"username" goe:"unique" binding:"required"`
	PasswordHash string    `json:"-"`
	Role         int       `json:"role"`
	BasePath     string    `json:"base_path"`
	Permission   int32     `json:"permission"`
	Avatar       string    `json:"avatar"` // 预设头像 key，见 AvatarPresets
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastLoginAt  time.Time `json:"last_login_at"`
}

// SetAvatar 选择预设头像；传入非法值时报错，不改数据。
func (u *User) SetAvatar(key string) error {
	if !IsValidAvatar(key) {
		return errors.Errorf("invalid avatar %q, must be one of %v", key, AvatarPresets)
	}
	u.Avatar = key
	return nil
}

// AvatarOrDefault 返回头像 key，未设置时给默认值，便于前端直接拼 URL。
func (u *User) AvatarOrDefault() string {
	if IsValidAvatar(u.Avatar) {
		return u.Avatar
	}
	return DefaultAvatar
}

// SetPassword 生成密码哈希。
//
// secret 可以是明文，也可以是客户端算好的静态哈希
// （当前 Flutter 端传 sha256(明文+"-"+固定盐)）；服务端一律再过一次 bcrypt，
// 这样即便请求被截获，库里躺着的仍然只是不可逆哈希。
func (u *User) SetPassword(secret string) error {
	if secret == "" {
		return errors.New("password is empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return errors.Wrap(err, "hash password")
	}
	u.PasswordHash = string(hash)
	return nil
}

// CheckPassword 校验密码。空哈希一律拒绝，避免未初始化账号被绕过。
func (u *User) CheckPassword(secret string) bool {
	if u.PasswordHash == "" || secret == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(secret)) == nil
}

// IsAdmin 是否管理员。
func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// CreateUser 新增账号。
func CreateUser(u *User) error {
	if u.Avatar == "" {
		u.Avatar = DefaultAvatar // 未选头像时给默认值，前端不必处理空值
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	u.UpdatedAt = u.CreatedAt
	return errors.WithStack(GetSchema().User.Insert().One(u))
}

// UpdateUser 更新账号（含改密后回写哈希）。
func UpdateUser(u *User) error {
	u.UpdatedAt = time.Now()
	return errors.WithStack(GetSchema().User.Save().One(u))
}

// GetUserByName 按用户名查账号，未找到返回 ErrRecordNotFound。
func GetUserByName(username string) (*User, error) {
	u, err := GetSchema().User.Where("username = ?", username).Select().One()
	if err != nil {
		if isNoRows(err) {
			return nil, ErrRecordNotFound
		}
		return nil, errors.Wrapf(err, "failed find user %q", username)
	}
	if u == nil {
		return nil, ErrRecordNotFound
	}
	return u, nil
}

// GetUserByID 按主键查账号。
func GetUserByID(id uint) (*User, error) {
	u, err := GetSchema().User.Where("id = ?", id).Select().One()
	if err != nil {
		if isNoRows(err) {
			return nil, ErrRecordNotFound
		}
		return nil, errors.Wrapf(err, "failed find user #%d", id)
	}
	if u == nil {
		return nil, ErrRecordNotFound
	}
	return u, nil
}

// CountUsers 统计账号数。用于首启引导：库里一个账号都没有时，
// 第一个注册者必须成为管理员，否则没人能触达 /api/storage/*，系统无法配置。
func CountUsers() (int, error) {
	all, err := GetSchema().User.Select().All()
	if err != nil {
		return 0, errors.Wrap(err, "failed count users")
	}
	return len(all), nil
}

// CountAdmins 统计管理员数量。
// 用于兜底：不允许删除或降级最后一个管理员，否则系统会锁死。
func CountAdmins() (int, error) {
	all, err := GetSchema().User.Where("role = ?", RoleAdmin).Select().All()
	if err != nil {
		return 0, errors.Wrap(err, "failed count admins")
	}
	return len(all), nil
}

// ListUsers 列出全部账号。
func ListUsers() ([]*User, error) {
	users, err := GetSchema().User.Select().All()
	if err != nil {
		return nil, errors.Wrap(err, "failed list users")
	}
	return users, nil
}

// DeleteUser 删除账号。
func DeleteUser(id uint) error {
	return errors.WithStack(GetSchema().User.Delete().Where("id = ?", id).Exec())
}

// TouchLogin 记录登录时间。
func TouchLogin(u *User) error {
	u.LastLoginAt = time.Now()
	return UpdateUser(u)
}

// NormalizeUsername 统一大小写与空白，避免同名账号绕过唯一约束。
func NormalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
