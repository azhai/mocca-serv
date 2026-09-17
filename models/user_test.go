package models

import (
	"path/filepath"
	"strings"
	"testing"
)

// openTestDB 每个用例一份独立库文件，避免相互污染。
func openTestDB(t *testing.T) {
	t.Helper()
	dbFile := filepath.Join(t.TempDir(), "test.db")
	if _, err := Open(dbFile); err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = Close() })
}

func TestPasswordNeverStoredInPlainText(t *testing.T) {
	openTestDB(t)

	u := &User{Username: "alice", Role: RoleGeneral}
	if err := u.SetPassword("hunter2"); err != nil {
		t.Fatalf("SetPassword 失败: %v", err)
	}

	if u.PasswordHash == "hunter2" {
		t.Fatal("库里存了明文密码")
	}
	if strings.Contains(u.PasswordHash, "hunter2") {
		t.Fatal("哈希里出现了明文片段")
	}
	// bcrypt 哈希形如 $2a$10$<22位盐><31位摘要>
	if !strings.HasPrefix(u.PasswordHash, "$2") {
		t.Errorf("不是 bcrypt 哈希: %q", u.PasswordHash)
	}
	if !u.CheckPassword("hunter2") {
		t.Error("正确密码应当通过校验")
	}
}

func TestCheckPasswordRejectsWrongSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{"完全不同", "wrong"},
		{"大小写不同", "Hunter2"},
		{"多一个字符", "hunter22"},
		{"空密码", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &User{Username: "bob"}
			if err := u.SetPassword("hunter2"); err != nil {
				t.Fatalf("SetPassword 失败: %v", err)
			}
			if u.CheckPassword(tt.secret) {
				t.Errorf("CheckPassword(%q) 应为 false", tt.secret)
			}
		})
	}
}

func TestEmptyHashNeverPasses(t *testing.T) {
	// 未设置密码的账号（哈希为空）不能被任何密码绕过
	u := &User{Username: "ghost"}
	if u.CheckPassword("") || u.CheckPassword("anything") {
		t.Error("空哈希账号不应通过校验")
	}
}

func TestSetPasswordRejectsEmpty(t *testing.T) {
	u := &User{Username: "carol"}
	if err := u.SetPassword(""); err == nil {
		t.Error("空密码应当报错")
	}
}

func TestUserCRUDRoundTrip(t *testing.T) {
	openTestDB(t)

	u := &User{Username: NormalizeUsername("  Dave  "), Role: RoleGeneral, BasePath: "/dave"}
	if err := u.SetPassword("pw"); err != nil {
		t.Fatalf("SetPassword 失败: %v", err)
	}
	if err := CreateUser(u); err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("插入后应回填主键")
	}

	got, err := GetUserByName("dave")
	if err != nil {
		t.Fatalf("GetUserByName 失败: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID = %d, want %d", got.ID, u.ID)
	}
	if !got.CheckPassword("pw") {
		t.Error("读回的账号应当能通过原密码校验")
	}
	if got.IsAdmin() {
		t.Error("RoleGeneral 不应是管理员")
	}

	// 改密后旧密码失效
	if err = got.SetPassword("new-pw"); err != nil {
		t.Fatalf("改密失败: %v", err)
	}
	if err = UpdateUser(got); err != nil {
		t.Fatalf("UpdateUser 失败: %v", err)
	}
	again, err := GetUserByID(got.ID)
	if err != nil {
		t.Fatalf("GetUserByID 失败: %v", err)
	}
	if !again.CheckPassword("new-pw") || again.CheckPassword("pw") {
		t.Error("改密后应只认新密码")
	}

	all, err := ListUsers()
	if err != nil || len(all) != 1 {
		t.Errorf("ListUsers = %d 条, err=%v, want 1", len(all), err)
	}

	if err = DeleteUser(got.ID); err != nil {
		t.Fatalf("DeleteUser 失败: %v", err)
	}
	if _, err = GetUserByID(got.ID); err != ErrRecordNotFound {
		t.Errorf("删除后应返回未找到，got %v", err)
	}
}

func TestDuplicateUsernameRejected(t *testing.T) {
	openTestDB(t)

	first := &User{Username: "eve", Role: RoleGeneral}
	if err := first.SetPassword("pw"); err != nil {
		t.Fatalf("SetPassword 失败: %v", err)
	}
	if err := CreateUser(first); err != nil {
		t.Fatalf("首次创建 eve 失败: %v", err)
	}

	// 同名再插一次：允许它报错，也允许它静默失败，但库里最终只能有一条
	second := &User{Username: "eve", Role: RoleGeneral}
	if err := second.SetPassword("pw"); err != nil {
		t.Fatalf("SetPassword 失败: %v", err)
	}
	if err := CreateUser(second); err != nil {
		t.Logf("重复用户名被拒绝（预期行为）: %v", err)
	}

	all, err := ListUsers()
	if err != nil {
		t.Fatalf("ListUsers 失败: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("用户名 eve 出现 %d 次，唯一约束未生效", len(all))
	}
}

func TestStorageAndSettingCRUD(t *testing.T) {
	openTestDB(t)

	s := &Storage{MountPath: "/movies", Driver: "Local", Addition: `{"root_folder_path":"/mnt"}`}
	if err := CreateStorage(s); err != nil {
		t.Fatalf("CreateStorage 失败: %v", err)
	}
	got, err := GetStorageByMountPath("/movies")
	if err != nil {
		t.Fatalf("GetStorageByMountPath 失败: %v", err)
	}
	if got.Driver != "Local" || got.Addition != s.Addition {
		t.Errorf("读回的存储不符: %+v", got)
	}

	if err = SetSetting(&Setting{Key: "allow_register", Value: "true", Type: "bool"}); err != nil {
		t.Fatalf("SetSetting 失败: %v", err)
	}
	setting, err := GetSetting("allow_register")
	if err != nil || setting.Value != "true" {
		t.Errorf("GetSetting = %+v, err=%v", setting, err)
	}
	if _, err = GetSetting("missing"); err != ErrRecordNotFound {
		t.Errorf("不存在的配置应返回未找到，got %v", err)
	}
}
