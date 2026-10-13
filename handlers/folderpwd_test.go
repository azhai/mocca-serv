package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// newRecorder 只是 httptest.NewRecorder 的短名，保持测试行短。
func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// setupProtected 造一个 /media/secret 受密码保护的存储。
func setupProtected(t *testing.T, e *echo.Echo, admin string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "open.mp4", "o")
	writeTestFile(t, root, "secret/hidden.mp4", "h")
	writeTestFile(t, root, "secret/sub/deep.mp4", "d")
	addStorage(t, "/media", "Local", root)

	if code, resp := call(t, e, http.MethodPost, "/api/folder/password",
		`{"path":"/media/secret","password":"clue"}`, admin); code != 200 {
		t.Fatalf("设置目录密码失败 code=%d msg=%v", code, resp["message"])
	}
	return root
}

func TestFolderPasswordProtectsSubtree(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	setupProtected(t, e, admin)

	// 未带密码
	if code, resp := call(t, e, http.MethodPost, "/api/fs/list",
		`{"path":"/media/secret"}`, admin); code != 403 {
		t.Fatalf("受保护目录无密码应 403，got %d msg=%v", code, resp["message"])
	}
	// 密码错误
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list",
		`{"path":"/media/secret","password":"wrong"}`, admin); code != 403 {
		t.Errorf("密码错误应 403，got %d", code)
	}
	// 密码正确
	if code, resp := call(t, e, http.MethodPost, "/api/fs/list",
		`{"path":"/media/secret","password":"clue"}`, admin); code != 200 {
		t.Errorf("密码正确应放行，got %d msg=%v", code, resp["message"])
	}

	// 子目录同样受保护（父目录设一次即覆盖整棵子树）
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list",
		`{"path":"/media/secret/sub"}`, admin); code != 403 {
		t.Errorf("子目录无密码应 403，got %d", code)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list",
		`{"path":"/media/secret/sub","password":"clue"}`, admin); code != 200 {
		t.Errorf("子目录带正确密码应放行，got %d", code)
	}

	// 未受保护的目录不受影响
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media"}`, admin); code != 200 {
		t.Errorf("未受保护目录应直接放行，got %d", code)
	}

	// 取详情也受保护
	if code, _ := call(t, e, http.MethodPost, "/api/fs/get",
		`{"path":"/media/secret/hidden.mp4"}`, admin); code != 403 {
		t.Errorf("取详情无密码应 403，got %d", code)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/fs/get",
		`{"path":"/media/secret/hidden.mp4","password":"clue"}`, admin); code != 200 {
		t.Errorf("取详情带密码应放行，got %d", code)
	}
}

func TestFolderPasswordProtectsStreaming(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	setupProtected(t, e, admin)

	// 取流没带密码：必须拦住，否则密码形同虚设
	req, _ := http.NewRequest(http.MethodGet, "/d/media/secret/hidden.mp4", nil)
	req.Header.Set("Authorization", admin)
	rec := newRecorder()
	e.ServeHTTP(rec, req)
	if !contains(rec.Body.String(), "403") {
		t.Errorf("取流无密码应被拒，got %s", rec.Body.String())
	}

	// 带正确密码可取到内容
	req, _ = http.NewRequest(http.MethodGet, "/d/media/secret/hidden.mp4?password=clue", nil)
	req.Header.Set("Authorization", admin)
	rec = newRecorder()
	e.ServeHTTP(rec, req)
	if rec.Body.String() != "h" {
		t.Errorf("带正确密码应取到内容，got %q", rec.Body.String())
	}
}

func TestFolderPasswordAdminOnlyAndClear(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	user := makeUser(t, e, "plain", "p", models.RoleGeneral)

	// 普通用户不能设密码
	if code, _ := call(t, e, http.MethodPost, "/api/folder/password",
		`{"path":"/media/x","password":"p"}`, user); code != 403 {
		t.Errorf("普通用户设目录密码应 403，got %d", code)
	}

	root := t.TempDir()
	writeTestFile(t, root, "d/a.mp4", "x")
	addStorage(t, "/media", "Local", root)

	call(t, e, http.MethodPost, "/api/folder/password", `{"path":"/media/d","password":"pw"}`, admin)
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media/d"}`, admin); code != 403 {
		t.Fatalf("设密后应需要密码，got %d", code)
	}

	// 查询状态
	_, resp := call(t, e, http.MethodGet, "/api/folder/status?path=/media/d/sub", "", "")
	if d, _ := resp["data"].(map[string]any); d["protected"] != true || d["protected_dir"] != "/media/d" {
		t.Errorf("状态查询应报出受保护父目录，got %v", d)
	}
	// 状态接口不能泄露哈希
	if contains(respBody(resp), "$2") {
		t.Error("状态接口泄露了密码哈希")
	}

	// 清除后恢复访问
	if code, _ := call(t, e, http.MethodPost, "/api/folder/password",
		`{"path":"/media/d","password":""}`, admin); code != 200 {
		t.Fatalf("清除目录密码应成功，got %d", code)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media/d"}`, admin); code != 200 {
		t.Errorf("清除后应可直接访问，got %d", code)
	}
}

// 目录密码清单：后台目录树拿它一次就能把所有带锁的目录标出来。
// 两条硬要求：只给管理员、只给路径（哈希不能从这条路上漏出去）。
func TestListFolderPasswords(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	user := makeUser(t, e, "plain", "p", models.RoleGeneral)

	root := t.TempDir()
	writeTestFile(t, root, "open/a.mp4", "a")
	writeTestFile(t, root, "secret/b.mp4", "b")
	writeTestFile(t, root, "secret/deeper/c.mp4", "c")
	addStorage(t, "/media", "Local", root)

	if code, _ := call(t, e, http.MethodGet, "/api/folder/passwords", "", user); code != 403 {
		t.Errorf("普通用户查清单应 403，got %d", code)
	}

	// 直接设过密的两级都要出现；继承而来的子目录**不**单列（清单是「谁设的」）
	call(t, e, http.MethodPost, "/api/folder/password", `{"path":"/media/secret","password":"k1"}`, admin)
	call(t, e, http.MethodPost, "/api/folder/password", `{"path":"/media/secret/deeper","password":"k2"}`, admin)
	_, resp := call(t, e, http.MethodGet, "/api/folder/passwords", "", admin)
	dirs := listedDirs(resp)
	if !hasDir(dirs, "/media/secret") || !hasDir(dirs, "/media/secret/deeper") {
		t.Errorf("清单应含两级设密目录，got %v", dirs)
	}
	if hasDir(dirs, "/media/open") || hasDir(dirs, "/media") {
		t.Errorf("没设过密码的目录不该出现在清单里，got %v", dirs)
	}
	if contains(respBody(resp), "$2") {
		t.Error("清单接口泄露了密码哈希")
	}

	// 清除后从清单消失（仍是幂等操作）
	call(t, e, http.MethodPost, "/api/folder/password", `{"path":"/media/secret/deeper","password":""}`, admin)
	_, resp = call(t, e, http.MethodGet, "/api/folder/passwords", "", admin)
	if hasDir(listedDirs(resp), "/media/secret/deeper") {
		t.Errorf("清除后应不在清单里，got %v", listedDirs(resp))
	}
}

// hasDir 清单里有没有这个路径（测里的 contains 是子串比对，这里要整项相等）。
func hasDir(dirs []string, want string) bool {
	for _, d := range dirs {
		if d == want {
			return true
		}
	}
	return false
}

// listedDirs 从信封里取出 dirs 字段的字符串切片。
func listedDirs(resp map[string]any) []string {
	d, _ := resp["data"].(map[string]any)
	arr, _ := d["dirs"].([]any)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestFolderPasswordNeverStoredInPlainText(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "d"), 0o755)
	addStorage(t, "/media", "Local", root)

	call(t, e, http.MethodPost, "/api/folder/password", `{"path":"/media/d","password":"plain-secret"}`, admin)

	s, err := models.GetSetting(models.FolderPwdPrefix + "/media/d")
	if err != nil {
		t.Fatalf("应写入设置项: %v", err)
	}
	if s.Value == "plain-secret" || contains(s.Value, "plain-secret") {
		t.Fatalf("目录密码被明文存储: %q", s.Value)
	}
	if len(s.Value) < 4 || s.Value[:2] != "$2" {
		t.Errorf("应为 bcrypt 哈希，got %q", s.Value)
	}
}
