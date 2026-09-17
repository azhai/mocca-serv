package models

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/pkg/errors"
)

// 首启管理员使用的固定用户名。口令由调用方传入（来自配置，见 config.AdminPassword）。
//
// 安全提示：默认口令属于「开箱可用」与安全之间的取舍 —— 只在库里
// **没有任何管理员**时才会创建，创建后打印醒目告警，部署后应立刻改密。
const DefaultAdminName = "admin"

// StaticHashSalt 与 APP 端 `ApiClient.staticHash` 和旧后端
// `model.StaticHashSalt` 保持一致；改这里会让老客户端登不进来。
const StaticHashSalt = "https://github.com/alist-org/alist"

// StaticHash 复刻客户端登录前算的那份静态哈希：sha256(明文 + "-" + 盐)，小写十六进制。
//
// 播种密码必须走它：APP 发送的是静态哈希，服务端再对它做 bcrypt 比对。
// 若把明文直接 bcrypt 进库，`admin/123456` 会永远登不进去（算出来的哈希对不上）。
func StaticHash(password string) string {
	sum := sha256.Sum256([]byte(password + "-" + StaticHashSalt))
	return hex.EncodeToString(sum[:])
}

// IsStaticHashText 判断字符串是否是客户端算好的静态哈希：64 位**小写**十六进制。
//
// 用在 API 边界挡「误传明文」：历史上后台的用户编辑表单就是发明文的，
// 服务端照样存了 bcrypt(明文)，那个账号从此永远登不进去，而且不报任何错。
// 这类静默损坏比报错难查得多，宁可在这里拒绝。
func IsStaticHashText(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ResetPassword 把 username 的口令重设为 plainPassword（明文）。
//
// 存在的原因：口令写坏或忘记后无法自助恢复 —— 库里是 bcrypt，不可逆；
// 而 EnsureAdmin 只在「一个管理员都没有」时才会动手，救不了单个账号。
// 它只通过 CLI 暴露（见 main.go 的 passwd 子命令），需要能登上服务器，
// 等价于已拥有机器权限，不额外扩大攻击面。
//
// 必须存 bcrypt(StaticHash(明文))：登录路径拿 CheckPassword(StaticHash(明文)) 比对。
func ResetPassword(username, plainPassword string) error {
	if plainPassword == "" {
		return errors.New("新口令为空")
	}
	u, err := GetUserByName(NormalizeUsername(username))
	if err != nil {
		return errors.Wrapf(err, "找不到账号 %q", username)
	}
	if err := u.SetPassword(StaticHash(plainPassword)); err != nil {
		return errors.Wrap(err, "重置口令")
	}
	if err := UpdateUser(u); err != nil {
		return errors.Wrap(err, "保存账号")
	}
	return nil
}

// EnsureAdmin 在库中没有任何管理员时创建管理员（用户名 DefaultAdminName），
// password 是明文口令（来自配置），返回是否真的创建/提权过。
//
// 必须在「连上库之后、服务启动之前」调用：否则新装的服务一个管理员都没有，
// `/api/storage/*` 全部 403，连存储挂载点都配不了（也就没有任何内容可看）。
//
// 若同名账号已存在（例如先前注册过普通用户 admin），把它提升为管理员，
// 而不是插入第二条同名记录 —— 用户名有唯一约束，硬插会直接失败。
func EnsureAdmin(password string) (created bool, err error) {
	if password == "" {
		return false, errors.New("默认管理员口令为空")
	}

	n, err := CountAdmins()
	if err != nil {
		return false, errors.Wrap(err, "count admins")
	}
	if n > 0 {
		return false, nil
	}

	if u, err := GetUserByName(DefaultAdminName); err == nil && u != nil {
		u.Role = RoleAdmin
		if err := UpdateUser(u); err != nil {
			return false, errors.Wrap(err, "promote existing user to admin")
		}
		return true, nil
	}

	u := &User{Username: DefaultAdminName, Role: RoleAdmin}
	if err := u.SetPassword(StaticHash(password)); err != nil {
		return false, errors.Wrap(err, "set default admin password")
	}
	if err := CreateUser(u); err != nil {
		return false, errors.Wrap(err, "create default admin")
	}
	return true, nil
}
