package handlers_test

import (
	"net/http"
	"testing"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// TestSeededAdminCanLogin 端到端：用配置里的口令播种出来的 admin 必须能真的登进后台。
// 这条覆盖的是「密码用哪种哈希入库」这个最容易搞错的点。
func TestSeededAdminCanLogin(t *testing.T) {
	e := newApp(t)

	created, err := models.EnsureAdmin(config.Cfg.AdminPassword)
	if err != nil {
		t.Fatalf("播种管理员失败: %v", err)
	}
	if !created {
		t.Fatal("空库应播种管理员")
	}

	// 按 APP 的方式登录：密码先算静态哈希再发送
	code, resp := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"admin","password":"`+models.StaticHash(config.Cfg.AdminPassword)+`"}`, "")
	if code != 200 {
		t.Fatalf("配置口令应能登录，got code=%d msg=%v", code, resp["message"])
	}
	data, _ := resp["data"].(map[string]any)
	token, _ := data["token"].(string)
	if token == "" {
		t.Fatal("登录未返回令牌")
	}

	// 且真的是管理员：管理员专属接口要能过
	if code, resp = call(t, e, http.MethodGet, "/api/user/list", "", token); code != 200 {
		t.Errorf("默认管理员应能访问管理员接口，got %d msg=%v", code, resp["message"])
	}

	// 明文密码不能登录（证明入库的是「静态哈希再 bcrypt」）
	if code, _ = call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"admin","password":"`+config.Cfg.AdminPassword+`"}`, ""); code != 401 {
		t.Errorf("用明文密码登录应失败，got %d", code)
	}
}

// TestSeededAdminBlocksFirstUserPromotion 播种之后，首个注册者不再自动成为管理员。
// 这是行为的连带变化，必须显式钉住。
func TestSeededAdminBlocksFirstUserPromotion(t *testing.T) {
	e := newApp(t)
	if _, err := models.EnsureAdmin(config.Cfg.AdminPassword); err != nil {
		t.Fatal(err)
	}

	pwd := models.StaticHash("p")
	_, resp := call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"later","password":"`+pwd+`"}`, "")
	d, _ := resp["data"].(map[string]any)
	if d["is_admin"] == true {
		t.Error("已有管理员时，新注册用户不该自动成为管理员")
	}
}

// adminToken 播种管理员并登录，返回令牌。供需要管理员权限的用例复用。
func adminToken(t *testing.T, e *echo.Echo) string {
	t.Helper()
	if _, err := models.EnsureAdmin(config.Cfg.AdminPassword); err != nil {
		t.Fatal(err)
	}
	code, resp := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"admin","password":"`+models.StaticHash(config.Cfg.AdminPassword)+`"}`, "")
	if code != 200 {
		t.Fatalf("管理员登录失败: code=%d msg=%v", code, resp["message"])
	}
	data, _ := resp["data"].(map[string]any)
	token, _ := data["token"].(string)
	if token == "" {
		t.Fatal("登录未返回令牌")
	}
	return token
}

// TestPlaintextPasswordRejected 客户端误传明文必须在 API 边界被挡：
// 放进去会存成 bcrypt(明文)，该账号**永远登不进去而且不报任何错**。
// 后台的用户编辑表单真这么干过（2026-09-17 的事故），所以这里要钉死。
func TestPlaintextPasswordRejected(t *testing.T) {
	e := newApp(t)
	token := adminToken(t, e)

	code, resp := call(t, e, http.MethodPost, "/api/user/create",
		`{"username":"plain","password":"plaintext","role":1}`, token)
	if code == 200 {
		t.Errorf("明文口令应被拒绝，got code=%d resp=%v", code, resp)
	}
	if _, err := models.GetUserByName("plain"); err == nil {
		t.Error("被拒的请求不该留下账号")
	}

	// 换成静态哈希则应当成功，而且这个新账号真的能登录
	good := models.StaticHash("pw/2")
	if code, resp = call(t, e, http.MethodPost, "/api/user/create",
		`{"username":"hashed","password":"`+good+`","role":1}`, token); code != 200 {
		t.Fatalf("静态哈希应能建号，got code=%d resp=%v", code, resp)
	}
	if code, resp = call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"hashed","password":"`+good+`"}`, ""); code != 200 {
		t.Errorf("新建用户应能登录，got code=%d msg=%v", code, resp["message"])
	}
}

// TestResetPasswordThenLogin 自救通道端到端：重置口令后立刻能登录。
func TestResetPasswordThenLogin(t *testing.T) {
	e := newApp(t)
	if _, err := models.EnsureAdmin(config.Cfg.AdminPassword); err != nil {
		t.Fatal(err)
	}

	const target = "Recovered/7"
	code, _ := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"admin","password":"`+models.StaticHash(target)+`"}`, "")
	if code == 200 {
		t.Fatal("重置前不该能用这个口令登录")
	}

	if err := models.ResetPassword(models.DefaultAdminName, target); err != nil {
		t.Fatalf("重置失败: %v", err)
	}

	code, resp := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"admin","password":"`+models.StaticHash(target)+`"}`, "")
	if code != 200 {
		t.Fatalf("重置后应能用新口令登录，got code=%d msg=%v", code, resp["message"])
	}
}
