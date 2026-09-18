package models

import (
	"strings"
	"testing"
)

// testPwd 测试用口令（真实默认值来自配置，见 config.DefaultAdminPassword）。
const testPwd = "Match/1"

// TestStaticHashMatchesClient 交叉验证静态哈希算法。
// 期望值由 Python 独立算出：sha256("123456-https://github.com/alist-org/alist")。
// 一旦有人改了盐或编码方式，APP 登录会全部失败，这里必须先炸。
func TestStaticHashMatchesClient(t *testing.T) {
	const want = "e166b45e39301021e897e3a6713e11171893217ad2901cf28c2c09c8d54e55d9"
	if got := StaticHash("123456"); got != want {
		t.Errorf("StaticHash(\"123456\") = %s，期望 %s（客户端算法：sha256(明文+\"-\"+盐)）", got, want)
	}
	if got := StaticHash(""); len(got) != 64 {
		t.Errorf("空密码也应给出 64 位十六进制，got %q", got)
	}
	// 不同明文必须得到不同哈希
	if StaticHash("123456") == StaticHash("123457") {
		t.Error("不同密码不应产生相同哈希")
	}
}

func TestEnsureAdminCreatesDefaultAdmin(t *testing.T) {
	openTestDB(t)

	created, err := EnsureAdmin(testPwd)
	if err != nil {
		t.Fatalf("EnsureAdmin 失败: %v", err)
	}
	if !created {
		t.Fatal("空库应创建默认管理员")
	}

	u, err := GetUserByName(DefaultAdminName)
	if err != nil {
		t.Fatalf("应能查到默认管理员: %v", err)
	}
	if !u.IsAdmin() {
		t.Errorf("角色应为管理员，got %d", u.Role)
	}

	// 关键：库里存的必须是「客户端静态哈希」再 bcrypt，
	// 所以用静态哈希校验要能通过，用明文校验必须失败。
	if !u.CheckPassword(StaticHash(testPwd)) {
		t.Errorf("admin/%s 应能通过校验（APP 发的是静态哈希）", testPwd)
	}
	if u.CheckPassword(testPwd) {
		t.Error("用明文不该通过——说明入库的不是静态哈希再 bcrypt")
	}
	// 且库里绝不能是明文
	if u.PasswordHash == testPwd {
		t.Fatal("密码被明文存储")
	}
}

// TestEnsureAdminUsesGivenPassword 口令来自配置，播种的必须是它而不是某个内置值。
func TestEnsureAdminUsesGivenPassword(t *testing.T) {
	openTestDB(t)

	const custom = "hunter2/OK"
	if _, err := EnsureAdmin(custom); err != nil {
		t.Fatalf("EnsureAdmin 失败: %v", err)
	}

	u, err := GetUserByName(DefaultAdminName)
	if err != nil {
		t.Fatal(err)
	}
	if !u.CheckPassword(StaticHash(custom)) {
		t.Error("应能用在配置里指定的口令登录")
	}
	if u.CheckPassword(StaticHash(testPwd)) {
		t.Error("不该还能用另一个口令登录")
	}
}

// TestEnsureAdminRejectsEmptyPassword 空口令必须报错而不是造出一个空密码管理员。
func TestEnsureAdminRejectsEmptyPassword(t *testing.T) {
	openTestDB(t)

	if _, err := EnsureAdmin(""); err == nil {
		t.Fatal("空口令应返回错误")
	}
	n, err := CountAdmins()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("出错时不该留下任何账号，got %d", n)
	}
}

func TestEnsureAdminIsIdempotent(t *testing.T) {
	openTestDB(t)

	for i := range 2 {
		created, err := EnsureAdmin(testPwd)
		if err != nil {
			t.Fatalf("第 %d 次 EnsureAdmin 失败: %v", i+1, err)
		}
		if i == 0 && !created {
			t.Error("首次应创建")
		}
		if i > 0 && created {
			t.Error("已存在管理员时不应重复创建")
		}
	}

	n, err := CountAdmins()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("管理员数量应为 1，got %d", n)
	}
	all, _ := ListUsers()
	if len(all) != 1 {
		t.Errorf("账号总数应为 1（不能出现同名重复），got %d", len(all))
	}
}

func TestEnsureAdminNoopWhenAdminExists(t *testing.T) {
	openTestDB(t)

	// 已有一个自建管理员
	other := &User{Username: "boss", Role: RoleAdmin}
	if err := other.SetPassword("secret"); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(other); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureAdmin(testPwd)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("已有管理员时不应再播种默认账号")
	}
	if _, err := GetUserByName(DefaultAdminName); err != ErrRecordNotFound {
		t.Errorf("不该创建 admin 账号，got err=%v", err)
	}
}

// TestEnsureAdminPromotesExistingUser 同名账号已存在时提升为管理员，
// 而不是硬插第二条（用户名有唯一约束，插入会失败）。
func TestEnsureAdminPromotesExistingUser(t *testing.T) {
	openTestDB(t)

	u := &User{Username: DefaultAdminName, Role: RoleGeneral}
	if err := u.SetPassword("mine"); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(u); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureAdmin(testPwd)
	if err != nil {
		t.Fatalf("EnsureAdmin 失败: %v", err)
	}
	if !created {
		t.Error("应报告执行了提权")
	}

	got, err := GetUserByName(DefaultAdminName)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAdmin() {
		t.Errorf("应被提升为管理员，got role=%d", got.Role)
	}
	// 原密码不能被改动
	if !got.CheckPassword("mine") {
		t.Error("提权不该改动原有密码")
	}
	all, _ := ListUsers()
	if len(all) != 1 {
		t.Errorf("不应产生重复账号，got %d", len(all))
	}
}

// TestResetPassword 重设口令后新口令可用、旧口令失效。
func TestResetPassword(t *testing.T) {
	openTestDB(t)
	if _, err := EnsureAdmin(testPwd); err != nil {
		t.Fatal(err)
	}

	const next = "NewPwd/9"
	if err := ResetPassword(DefaultAdminName, next); err != nil {
		t.Fatalf("ResetPassword 失败: %v", err)
	}

	u, err := GetUserByName(DefaultAdminName)
	if err != nil {
		t.Fatal(err)
	}
	// 存进去的必须是 bcrypt(StaticHash(新口令))，登录时才能用静态哈希比对通过
	if !u.CheckPassword(StaticHash(next)) {
		t.Error("重置后应能用新口令登录")
	}
	if u.CheckPassword(StaticHash(testPwd)) {
		t.Error("旧口令不该还能登录")
	}
	// 反向断言：库里绝不能是 bcrypt(明文) —— 那样账号将永远登不进去
	if u.CheckPassword(next) {
		t.Error("用明文比对竟然通过，说明存成了 bcrypt(明文)")
	}
}

// TestResetPasswordRejects 空口令与不存在的账号都要报错，且不动现有口令。
func TestResetPasswordRejects(t *testing.T) {
	openTestDB(t)
	if _, err := EnsureAdmin(testPwd); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ user, pwd string }{
		{DefaultAdminName, ""}, // 空口令
		{"nobody", "whatever"}, // 账号不存在
	} {
		if err := ResetPassword(tc.user, tc.pwd); err == nil {
			t.Errorf("user=%q pwd=%q 应报错", tc.user, tc.pwd)
		}
	}

	u, err := GetUserByName(DefaultAdminName)
	if err != nil {
		t.Fatal(err)
	}
	if !u.CheckPassword(StaticHash(testPwd)) {
		t.Error("失败的重置不该改动原口令")
	}
}

// TestIsStaticHashText 只有 64 位小写十六进制才算合法静态哈希。
// 它是「挡住误传明文」的那道闸，判宽了等于没挡。
func TestIsStaticHashText(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{StaticHash("x"), true},
		{StaticHash(""), true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("0", 64), true},
		{"", false},
		{"Match/1", false},               // 明文
		{"hunter2", false},               // 明文
		{strings.Repeat("A", 64), false}, // 大写不算（客户端产出的就是小写）
		{strings.Repeat("g", 64), false}, // 非十六进制字符
		{strings.Repeat("a", 63), false}, // 少一位
		{strings.Repeat("a", 65), false}, // 多一位
	}
	for _, c := range cases {
		if got := IsStaticHashText(c.in); got != c.want {
			t.Errorf("IsStaticHashText(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
