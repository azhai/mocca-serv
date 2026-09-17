package handlers_test

import (
	"net/http"
	"testing"

	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/models"
)

func TestFirstUserBecomesAdmin(t *testing.T) {
	e := newApp(t)

	// 空库：未初始化
	code, resp := call(t, e, http.MethodGet, "/api/init/status", "", "")
	if code != 200 {
		t.Fatalf("init/status 应成功，got %d", code)
	}
	st, _ := resp["data"].(map[string]any)
	if st["initialized"] != false {
		t.Errorf("空库应为未初始化，got %v", st["initialized"])
	}

	// 第一个注册者必须是管理员，否则没人能配存储
	code, resp = call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"boss","password":"`+pwdHash("p")+`"}`, "")
	if code != 200 {
		t.Fatalf("首个注册应成功，got %d", code)
	}
	data, _ := resp["data"].(map[string]any)
	if data["is_admin"] != true {
		t.Fatalf("第一个注册者应为管理员，got %v", data)
	}
	if data["role"] != float64(models.RoleAdmin) {
		t.Errorf("role = %v, want %d", data["role"], models.RoleAdmin)
	}

	// 之后注册的是普通用户
	_, resp = call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"nobody","password":"`+pwdHash("p")+`"}`, "")
	if d, _ := resp["data"].(map[string]any); d["is_admin"] != false {
		t.Errorf("第二个注册者不该是管理员，got %v", d)
	}

	// 已初始化
	_, resp = call(t, e, http.MethodGet, "/api/init/status", "", "")
	if st, _ = resp["data"].(map[string]any); st["initialized"] != true {
		t.Errorf("已有账号后应为已初始化，got %v", st["initialized"])
	}
}

func TestFirstAdminCanActuallyConfigureStorage(t *testing.T) {
	// 端到端闭环：首启注册 → 用返回的账号登录 → 建存储成功
	e := newApp(t)
	root := t.TempDir()

	call(t, e, http.MethodPost, "/api/auth/register", `{"username":"boss","password":"`+pwdHash("p")+`"}`, "")
	_, resp := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"boss","password":"`+pwdHash("p")+`"}`, "")
	data, _ := resp["data"].(map[string]any)
	token, _ := data["token"].(string)

	code, resp := call(t, e, http.MethodPost, "/api/storage/create",
		`{"mount_path":"/m","driver":"Local","addition":"{\"root_folder_path\":\"`+root+`\"}"}`, token)
	if code != 200 {
		t.Fatalf("首个管理员应能建存储，got code=%d msg=%v", code, resp["message"])
	}
}

func TestAllowRegisterCanBeTurnedOff(t *testing.T) {
	e := newApp(t)
	call(t, e, http.MethodPost, "/api/auth/register", `{"username":"boss","password":"`+pwdHash("p")+`"}`, "")

	// 关掉注册（模拟后台设置）
	if err := models.SetSettingBool(handlers.SettingAllowRegister, false); err != nil {
		t.Fatalf("写设置失败: %v", err)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"late","password":"`+pwdHash("p")+`"}`, ""); code != 403 {
		t.Errorf("关闭注册后应返回 403，got %d", code)
	}
	// 状态接口也应反映出来
	_, resp := call(t, e, http.MethodGet, "/api/init/status", "", "")
	if st, _ := resp["data"].(map[string]any); st["allow_register"] != false {
		t.Errorf("allow_register 应为 false，got %v", st["allow_register"])
	}

	// 再打开就恢复
	if err := models.SetSettingBool(handlers.SettingAllowRegister, true); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"late","password":"`+pwdHash("p")+`"}`, ""); code != 200 {
		t.Errorf("重新开放注册后应成功，got %d", code)
	}
}
