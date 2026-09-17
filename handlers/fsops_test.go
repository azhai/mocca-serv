package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// writeTestFile 在 root 下按 rel 写文件，自动建父目录。
func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestFsRenameAndMove(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	addStorage(t, "/media", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	writeTestFile(t, root, "a.mp4", "video")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 改名
	if code, resp := call(t, e, http.MethodPost, "/api/fs/rename",
		`{"path":"/media/a.mp4","name":"b.mp4"}`, admin); code != 200 {
		t.Fatalf("改名应成功，got %d msg=%v", code, resp["message"])
	}
	if exists(filepath.Join(root, "a.mp4")) || !exists(filepath.Join(root, "b.mp4")) {
		t.Error("改名后旧文件应消失、新文件应存在")
	}

	// 移动
	if code, resp := call(t, e, http.MethodPost, "/api/fs/move",
		`{"path":"/media/b.mp4","dst_dir":"/media/sub"}`, admin); code != 200 {
		t.Fatalf("移动应成功，got %d msg=%v", code, resp["message"])
	}
	if exists(filepath.Join(root, "b.mp4")) || !exists(filepath.Join(root, "sub", "b.mp4")) {
		t.Error("移动后文件应在目标目录里")
	}

	// 普通用户不能改名/移动
	user := makeUser(t, e, "plain", "p", models.RoleGeneral)
	if code, _ := call(t, e, http.MethodPost, "/api/fs/rename",
		`{"path":"/media/sub/b.mp4","name":"c.mp4"}`, user); code != 403 {
		t.Errorf("普通用户改名应 403，got %d", code)
	}
	if code, _ := call(t, e, http.MethodPost, "/api/fs/move",
		`{"path":"/media/sub/b.mp4","dst_dir":"/media"}`, user); code != 403 {
		t.Errorf("普通用户移动应 403，got %d", code)
	}

	// 参数缺失
	if code, _ := call(t, e, http.MethodPost, "/api/fs/rename", `{"path":"/media/sub/b.mp4"}`, admin); code != 400 {
		t.Errorf("缺 name 应 400，got %d", code)
	}
}

func TestFsMoveRejectsCrossStorage(t *testing.T) {
	e := newApp(t)
	rootA := t.TempDir()
	rootB := t.TempDir()
	addStorage(t, "/a", "Local", rootA)
	addStorage(t, "/b", "Local", rootB)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	writeTestFile(t, rootA, "x.mp4", "v")
	// 跨存储移动没有廉价实现，必须明确报错而不是静默变成慢速拷贝
	code, resp := call(t, e, http.MethodPost, "/api/fs/move",
		`{"path":"/a/x.mp4","dst_dir":"/b"}`, admin)
	if code != 400 {
		t.Fatalf("跨存储移动应 400，got %d", code)
	}
	if !contains(respBody(resp), "跨存储") {
		t.Errorf("提示应说明不支持跨存储，got %s", respBody(resp))
	}
	if !exists(filepath.Join(rootA, "x.mp4")) {
		t.Error("被拒绝时源文件不该被动过")
	}
}

var _ = echo.Context{}
