package handlers_test

import (
	"net/http"
	"testing"

	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// loginAs 注册（首个即管理员）并登录，返回令牌。
func loginAs(t *testing.T, e *echo.Echo, name string) string {
	t.Helper()
	if code, _ := call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"`+name+`","password":"`+pwdHash("p")+`"}`, ""); code != 200 {
		t.Fatalf("注册 %s 失败，code=%d", name, code)
	}
	_, resp := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"`+name+`","password":"`+pwdHash("p")+`"}`, "")
	data, _ := resp["data"].(map[string]any)
	token, _ := data["token"].(string)
	if token == "" {
		t.Fatalf("登录 %s 未拿到令牌", name)
	}
	return token
}

func TestSettingListShowsDefaults(t *testing.T) {
	e := newApp(t)
	token := loginAs(t, e, "boss")

	code, resp := call(t, e, http.MethodGet, "/api/setting/list", "", token)
	if code != 200 {
		t.Fatalf("setting/list 应成功，got code=%d msg=%v", code, resp["message"])
	}
	list, _ := resp["data"].([]any)
	if len(list) == 0 {
		t.Fatal("选项列表不该为空")
	}
	got := map[string]map[string]any{}
	for _, it := range list {
		o, _ := it.(map[string]any)
		k, _ := o["key"].(string)
		got[k] = o
	}

	// FS Watch 是本需求的核心：默认必须是关的
	fw, ok := got[models.SettingFsWatch]
	if !ok {
		t.Fatalf("选项列表缺少 %s，got %v", models.SettingFsWatch, got)
	}
	if fw["value"] != false {
		t.Errorf("fs_watch 默认应为 false，got %v", fw["value"])
	}
	if fw["label"] == "" || fw["hint"] == "" {
		t.Errorf("选项应带 label/hint，got %v", fw)
	}
	// 另外两个开关也应出现在列表里（后台要能一起看到）
	for _, k := range []string{handlers.SettingAllowGuest, handlers.SettingAllowRegister} {
		if _, ok := got[k]; !ok {
			t.Errorf("选项列表缺少 %s", k)
		}
	}
}

func TestUpdateSettingPersists(t *testing.T) {
	e := newApp(t)
	token := loginAs(t, e, "boss")

	code, resp := call(t, e, http.MethodPost, "/api/setting/update",
		`{"key":"`+handlers.SettingAllowGuest+`","value":false}`, token)
	if code != 200 {
		t.Fatalf("写设置应成功，got code=%d msg=%v", code, resp["message"])
	}
	if d, _ := resp["data"].(map[string]any); d["value"] != false {
		t.Errorf("返回值应为 false，got %v", d)
	}
	// 落库（不是只回显）
	if models.SettingBool(handlers.SettingAllowGuest, true) {
		t.Error("allow_guest 应已落库为 false")
	}
	// 列表也应反映
	_, resp = call(t, e, http.MethodGet, "/api/setting/list", "", token)
	for _, it := range resp["data"].([]any) {
		o, _ := it.(map[string]any)
		if o["key"] == handlers.SettingAllowGuest && o["value"] != false {
			t.Errorf("列表里的 allow_guest 应为 false，got %v", o["value"])
		}
	}
}

// TestFsWatchToggleRoundTrip 开关能写进库，且启停回调不炸（无挂载点时监控立即返回）。
func TestFsWatchToggleRoundTrip(t *testing.T) {
	e := newApp(t)
	token := loginAs(t, e, "boss")

	if models.SettingBool(models.SettingFsWatch, false) {
		t.Fatal("测试起点：fs_watch 不该是开的")
	}
	code, resp := call(t, e, http.MethodPost, "/api/setting/update",
		`{"key":"`+models.SettingFsWatch+`","value":true}`, token)
	if code != 200 {
		t.Fatalf("打开 FS Watch 应成功，got code=%d msg=%v", code, resp["message"])
	}
	if !models.SettingBool(models.SettingFsWatch, false) {
		t.Error("fs_watch 应已落库为 true")
	}
	// 关回去，顺带验证 Stop 幂等（连关两次不该出错）
	for i := 0; i < 2; i++ {
		if code, resp = call(t, e, http.MethodPost, "/api/setting/update",
			`{"key":"`+models.SettingFsWatch+`","value":false}`, token); code != 200 {
			t.Fatalf("第 %d 次关闭 FS Watch 失败，code=%d msg=%v", i+1, code, resp["message"])
		}
	}
	if models.SettingBool(models.SettingFsWatch, true) {
		t.Error("fs_watch 应已落库为 false")
	}
}

// TestUpdateSettingRejectsUnknownKey settings 表是通用键值表，
// 后台只能改白名单内的键，否则等于开了个任意写入的口子。
func TestUpdateSettingRejectsUnknownKey(t *testing.T) {
	e := newApp(t)
	token := loginAs(t, e, "boss")

	if code, _ := call(t, e, http.MethodPost, "/api/setting/update",
		`{"key":"jwt_secret","value":true}`, token); code != 400 {
		t.Fatalf("未知键应被拒，got code=%d", code)
	}
	if _, err := models.GetSetting("jwt_secret"); err == nil {
		t.Error("未知键不该被写进库")
	}
}

// TestSettingRequiresAdmin 普通用户碰不到全局选项。
func TestSettingRequiresAdmin(t *testing.T) {
	e := newApp(t)
	loginAs(t, e, "boss")               // 首个账号 = 管理员
	user := loginAs(t, e, "plain_user") // 之后的是普通用户

	if code, _ := call(t, e, http.MethodGet, "/api/setting/list", "", user); code != 403 {
		t.Errorf("普通用户读选项应 403，got %d", code)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/setting/update",
		`{"key":"`+models.SettingFsWatch+`","value":true}`, user); code != 403 {
		t.Errorf("普通用户写选项应 403，got %d", code)
	}
}
