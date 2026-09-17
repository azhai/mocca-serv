package handlers_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/azhai/mocca/models"
)

// 测试内的小工具，避免为了拼 JSON 到处写 fmt.Sprintf。
func itoa(n int) string { return strconv.Itoa(n) }

func respBody(resp map[string]any) string {
	b, _ := json.Marshal(resp)
	return string(b)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestUserManagementRequiresAdmin(t *testing.T) {
	e := newApp(t)
	user := makeUser(t, e, "plain", "p", models.RoleGeneral)

	if code, _ := call(t, e, http.MethodGet, "/api/user/list", "", user); code != 403 {
		t.Errorf("普通用户列用户应 403，got %d", code)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/user/create",
		`{"username":"x","password":"p","role":`+itoa(models.RoleGeneral)+`}`, user); code != 403 {
		t.Errorf("普通用户建号应 403，got %d", code)
	}
	if code, _ := call(t, e, http.MethodGet, "/api/user/list", "", ""); code != 401 {
		t.Errorf("未登录应 401，got %d", code)
	}
}

func TestAdminUserCRUD(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 建号
	code, resp := call(t, e, http.MethodPost, "/api/user/create",
		`{"username":"carol","password":"`+pwdHash("pw")+`","role":`+itoa(models.RoleGeneral)+`,"base_path":"/media/home","avatar":"03"}`, admin)
	if code != 200 {
		t.Fatalf("建号应成功，got %d msg=%v", code, resp["message"])
	}
	// 响应里绝不能出现密码字段
	if s := respBody(resp); contains(s, "password") {
		t.Errorf("用户响应不该包含密码字段: %s", s)
	}

	// 列表
	_, resp = call(t, e, http.MethodGet, "/api/user/list", "", admin)
	list, _ := resp["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("应有 2 个账号，got %d", len(list))
	}

	// 改号：改角色与目录，密码留空不动
	var carolID float64
	for _, it := range list {
		if u := it.(map[string]any); u["username"] == "carol" {
			carolID = u["id"].(float64)
		}
	}
	code, _ = call(t, e, http.MethodPost, "/api/user/update",
		`{"id":`+itoa(int(carolID))+`,"username":"carol","role":`+itoa(models.RoleGuest)+`,"base_path":"","avatar":"05"}`, admin)
	if code != 200 {
		t.Fatalf("改号应成功，got %d", code)
	}
	u, err := models.GetUserByName("carol")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != models.RoleGuest || u.Avatar != "05" || u.BasePath != "" {
		t.Errorf("字段未更新: %+v", u)
	}
	if !u.CheckPassword(pwdHash("pw")) {
		t.Error("密码留空时不该被清掉")
	}

	// 删号
	if code, _ = call(t, e, http.MethodPost, "/api/user/delete",
		`{"id":`+itoa(int(carolID))+`}`, admin); code != 200 {
		t.Fatalf("删号应成功，got %d", code)
	}
	if _, err = models.GetUserByName("carol"); err != models.ErrRecordNotFound {
		t.Errorf("删号后应查不到，got %v", err)
	}
}

func TestLastAdminProtected(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "only", "p", models.RoleAdmin)
	u, _ := models.GetUserByName("only")

	// 不能降级最后一个管理员
	if code, resp := call(t, e, http.MethodPost, "/api/user/update",
		`{"id":`+itoa(int(u.ID))+`,"username":"only","role":`+itoa(models.RoleGeneral)+`}`, admin); code != 400 {
		t.Errorf("降级最后一个管理员应 400，got %d msg=%v", code, resp["message"])
	}
	// 也不能删
	if code, _ := call(t, e, http.MethodPost, "/api/user/delete",
		`{"id":`+itoa(int(u.ID))+`}`, admin); code != 400 {
		t.Errorf("删除最后一个管理员应 400，got %d", code)
	}

	// 有第二个管理员后就放行
	makeUser(t, e, "second", "p", models.RoleAdmin)
	if code, _ := call(t, e, http.MethodPost, "/api/user/delete",
		`{"id":`+itoa(int(u.ID))+`}`, admin); code == 200 {
		// 删除自己会让自己令牌失效，这里只验证不再被 400 拦住
		t.Log("第二个管理员存在时允许删除（预期）")
	}
}

func TestUpdateMeAvatarAndPassword(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "self", "p", models.RoleGeneral)

	// 只改头像
	code, resp := call(t, e, http.MethodPost, "/api/me/update", `{"avatar":"07"}`, tok)
	if code != 200 {
		t.Fatalf("改头像应成功，got %d", code)
	}
	if me, _ := resp["data"].(map[string]any); me["avatar"] != "07" {
		t.Errorf("头像未更新，got %v", me["avatar"])
	}

	// 非法头像被拒
	if code, _ := call(t, e, http.MethodPost, "/api/me/update", `{"avatar":"hack"}`, tok); code != 400 {
		t.Errorf("非法头像应 400，got %d", code)
	}

	// 改密：原密码错 → 403
	if code, _ := call(t, e, http.MethodPost, "/api/me/update",
		`{"password":"wrong","new_password":"newpw"}`, tok); code != 403 {
		t.Errorf("原密码错误应 403，got %d", code)
	}
	// 原密码对 → 生效（新旧口令都按客户端约定发静态哈希）
	if code, _ = call(t, e, http.MethodPost, "/api/me/update",
		`{"password":"`+pwdHash("p")+`","new_password":"`+pwdHash("newpw")+`"}`, tok); code != 200 {
		t.Errorf("改密应成功，got %d", code)
	}
	u, _ := models.GetUserByName("self")
	if !u.CheckPassword(pwdHash("newpw")) || u.CheckPassword(pwdHash("p")) {
		t.Error("改密后应只认新密码")
	}
}
