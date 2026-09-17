package handlers_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// addStorage 挂一个本地存储，root 为实际目录。
func addStorage(t *testing.T, mount, driver, root string) {
	t.Helper()
	if err := models.CreateStorage(&models.Storage{
		MountPath: mount,
		Driver:    driver,
		Addition:  `{"root_folder_path":"` + root + `"}`,
	}); err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
}

// postFiles 构造 multipart 上传请求并打到指定路径。
func postFiles(t *testing.T, e *echo.Echo, url, token string,
	field string, files map[string]string) (int, map[string]any) {
	t.Helper()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for name, content := range files {
		part, err := w.CreateFormFile(field, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, url, body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法信封: %v, body=%s", err, rec.Body.String())
	}
	code, _ := resp["code"].(float64)
	return int(code), resp
}

func TestUploadOneWritesFile(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	tok := makeUser(t, e, "up", "p", models.RoleAdmin)
	addStorage(t, "/up", "Local", root)

	code, _ := postFiles(t, e, "/api/fs/put?path=/up", tok, "file",
		map[string]string{"a.txt": "hello"})
	if code != 200 {
		t.Fatalf("上传应成功，got code=%d", code)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("文件内容不符: %q, err=%v", got, err)
	}
}

func TestUploadBatchWritesAllFiles(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	tok := makeUser(t, e, "up2", "p", models.RoleAdmin)
	addStorage(t, "/up", "Local", root)

	code, resp := postFiles(t, e, "/api/fs/upload?path=/up", tok, "files",
		map[string]string{"one.txt": "111", "two.txt": "222"})
	if code != 200 {
		t.Fatalf("批量上传应成功，got code=%d", code)
	}
	results, _ := resp["data"].([]any)
	if len(results) != 2 {
		t.Fatalf("应返回 2 条结果，got %d", len(results))
	}
	for name, want := range map[string]string{"one.txt": "111", "two.txt": "222"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, err=%v, want %q", name, got, err, want)
		}
	}
}

func TestUploadRejectsNonAdminAndEmpty(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	admin := makeUser(t, e, "ad", "p", models.RoleAdmin)
	guest := makeUser(t, e, "gu", "p", models.RoleGeneral)
	addStorage(t, "/up", "Local", root)

	// 非管理员 403
	if code, _ := postFiles(t, e, "/api/fs/put?path=/up", guest, "file",
		map[string]string{"x.txt": "x"}); code != 403 {
		t.Errorf("非管理员上传应返回 403，got %d", code)
	}
	// 未登录 401
	if code, _ := postFiles(t, e, "/api/fs/put?path=/up", "", "file",
		map[string]string{"x.txt": "x"}); code != 401 {
		t.Errorf("未登录上传应返回 401，got %d", code)
	}
	// 空表单 400
	if code, _ := postFiles(t, e, "/api/fs/put?path=/up", admin, "file",
		map[string]string{}); code != 400 {
		t.Errorf("空表单应返回 400，got %d", code)
	}
}

func TestUploadBlocksPathTraversal(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	tok := makeUser(t, e, "up3", "p", models.RoleAdmin)
	addStorage(t, "/up", "Local", root)

	// 文件名里的 ../ 必须被剥掉：文件只能落在存储根内
	postFiles(t, e, "/api/fs/put?path=/up", tok, "file",
		map[string]string{"../../escape.txt": "bad"})

	if _, err := os.Stat(filepath.Join(root, "escape.txt")); err != nil {
		t.Errorf("escape.txt 应被降到存储根内，got err=%v", err)
	}
	// 关键：绝不能写到存储根的上一级去
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); err == nil {
		t.Fatal("路径穿越成功：文件写到了存储根之外")
	}
}
