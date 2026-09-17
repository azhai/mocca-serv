package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// listNames 调 fs/list 并返回名字集合。
func listNames(t *testing.T, e *echo.Echo, path, token string) map[string]bool {
	t.Helper()
	code, resp := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"`+path+`"}`, token)
	if code != 200 {
		t.Fatalf("列目录失败 code=%d msg=%v", code, resp["message"])
	}
	data, _ := resp["data"].(map[string]any)
	content, _ := data["content"].([]any)
	names := map[string]bool{}
	for _, it := range content {
		if m, ok := it.(map[string]any); ok {
			names[m["name"].(string)] = true
		}
	}
	return names
}

func TestBasePathIsolation(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	// root/home 是普通用户的专属目录，root 下还有一个他不该看到的文件
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "home", "mine.mp4"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "others.mp4"), []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}
	addStorage(t, "/media", "Local", root)

	// 普通用户，限定在 /media/home
	tok := makeUser(t, e, "alice", "p", models.RoleGeneral)
	u, err := models.GetUserByName("alice")
	if err != nil {
		t.Fatal(err)
	}
	u.BasePath = "/media/home"
	if err = models.UpdateUser(u); err != nil {
		t.Fatal(err)
	}

	names := listNames(t, e, "/", tok)
	if !names["mine.mp4"] {
		t.Errorf("应能看到自己目录下的文件，got %v", names)
	}
	if names["others.mp4"] {
		t.Errorf("不该看到 base_path 之外的文件，got %v", names)
	}

	// 管理员不受限
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	if names = listNames(t, e, "/media", admin); !names["others.mp4"] {
		t.Errorf("管理员应看到全部，got %v", names)
	}

	// 越界路径必须被拒：path.Join 会把 ".." 归一化，只做前缀拼接是挡不住的
	for _, escape := range []string{"/../others.mp4", "/../../etc", "/home/../../others.mp4"} {
		code, resp := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"`+escape+`"}`, tok)
		if code != 401 {
			t.Errorf("越界路径 %q 应被拒（401），got code=%d resp=%v", escape, code, resp)
		}
	}
}

func TestGuestBrowseFollowsSetting(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.mp4"), []byte("x"), 0o644)
	addStorage(t, "/media", "Local", root)

	// 默认允许游客
	if names := listNames(t, e, "/media", ""); !names["a.mp4"] {
		t.Errorf("默认应允许游客浏览，got %v", names)
	}

	// 关掉后游客被拒
	if err := models.SetSettingBool(handlers.SettingAllowGuest, false); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media"}`, ""); code != 401 {
		t.Errorf("关闭游客浏览后应返回 401，got %d", code)
	}
}

func TestFsRemoveDeletesFile(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	target := filepath.Join(root, "gone.mp4")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	addStorage(t, "/media", "Local", root)

	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	if code, resp := call(t, e, http.MethodPost, "/api/fs/remove",
		`{"path":"/media/gone.mp4"}`, admin); code != 200 {
		t.Fatalf("删除应成功，got %d msg=%v", code, resp["message"])
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("文件应已被删除，err=%v", err)
	}

	// 普通用户不能删
	user := makeUser(t, e, "bob", "p", models.RoleGeneral)
	if code, _ := call(t, e, http.MethodPost, "/api/fs/remove",
		`{"path":"/media/gone.mp4"}`, user); code != 403 {
		t.Errorf("普通用户删除应返回 403，got %d", code)
	}
}
