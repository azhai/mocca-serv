package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/models"
	"github.com/azhai/mocca/routes"
	"github.com/labstack/echo/v5"
)

// newApp 起一个带临时库的完整应用。
func newApp(t *testing.T) *echo.Echo {
	t.Helper()
	dir := t.TempDir()
	config.Cfg = &config.Config{
		Addr:           ":0",
		DataDir:        dir,
		DBFile:         filepath.Join(dir, "test.db"),
		JWTSecret:      "unit-test-secret",
		TokenExpiresIn: 48,
		AllowRegister:  true,
		AdminPassword:  config.DefaultAdminPassword,
	}
	if _, err := models.Open(config.Cfg.DBFile); err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = models.Close() })

	e := echo.New()
	routes.SetupAPIRoutes(e)
	// 管理接口单独注册：纯 API 构建（-tags noweb）里 SetupAPIRoutes 不挂它们，
	// 但处理器本身两种构建都编译。测试要覆盖的是处理器逻辑，所以无条件补上 ——
	// 「纯 API 版确实不注册管理接口」由 routes 包自己的测试断言。
	routes.SetupAdminRoutes(e)
	return e
}

// call 发起一次请求并解析信封。
func call(t *testing.T, e *echo.Echo, method, path, body, token string) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s 的 HTTP 状态码应为 200，got %d", method, path, rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法信封: %v, body=%s", err, rec.Body.String())
	}
	code, _ := resp["code"].(float64)
	return int(code), resp
}

func TestPingReturnsPong(t *testing.T) {
	e := newApp(t)
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Body.String() != "pong" {
		t.Errorf("GET /ping = %q, want pong", rec.Body.String())
	}
}

func TestAuthFlowRegisterLoginMe(t *testing.T) {
	e := newApp(t)
	pw := pwdHash("fake")

	// 注册
	if code, _ := call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"alice","password":"`+pw+`"}`, ""); code != 200 {
		t.Fatalf("注册应成功，got code=%d", code)
	}
	// 重名注册被拒
	if code, _ := call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"alice","password":"`+pw+`"}`, ""); code != 400 {
		t.Errorf("重名注册应返回 400，got %d", code)
	}

	// 登录拿令牌
	code, resp := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"alice","password":"`+pw+`"}`, "")
	if code != 200 {
		t.Fatalf("登录应成功，got code=%d", code)
	}
	data, _ := resp["data"].(map[string]any)
	token, _ := data["token"].(string)
	if token == "" {
		t.Fatal("登录成功但未返回令牌")
	}

	// 带令牌取当前用户
	code, resp = call(t, e, http.MethodGet, "/api/me", "", token)
	if code != 200 {
		t.Fatalf("me 应成功，got code=%d", code)
	}
	me, _ := resp["data"].(map[string]any)
	if me["username"] != "alice" {
		t.Errorf("me.username = %v, want alice", me["username"])
	}
	if me["avatar"] != models.DefaultAvatar {
		t.Errorf("新账号应带默认头像，got %v", me["avatar"])
	}
}

func TestAuthRejectsBadCredentials(t *testing.T) {
	e := newApp(t)
	call(t, e, http.MethodPost, "/api/auth/register",
		`{"username":"bob","password":"right-hash"}`, "")

	// 密码错误
	if code, _ := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"bob","password":"wrong"}`, ""); code != 401 {
		t.Errorf("密码错误应返回 401，got %d", code)
	}
	// 用户不存在（与密码错误同文案，不暴露账号是否存在）
	if code, _ := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"nobody","password":"x"}`, ""); code != 401 {
		t.Errorf("账号不存在应返回 401，got %d", code)
	}
	// 未登录访问 me
	if code, _ := call(t, e, http.MethodGet, "/api/me", "", ""); code != 401 {
		t.Errorf("未登录访问 me 应返回 401，got %d", code)
	}
	// 伪造令牌
	if code, _ := call(t, e, http.MethodGet, "/api/me", "", "not-a-real-token"); code != 401 {
		t.Errorf("伪造令牌应返回 401，got %d", code)
	}
}

func TestFsListAndGet(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()

	// 造两个文件
	writeFile(t, filepath.Join(root, "a.mp4"), []byte("video"))
	writeFile(t, filepath.Join(root, "b.txt"), []byte("hello"))

	// 挂一个存储，根指向临时目录
	if err := models.CreateStorage(&models.Storage{
		MountPath: "/media", Driver: "Local",
		Addition: `{"root_folder_path":"` + root + `"}`,
	}); err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}

	// 先登录（fs 是可选鉴权，这里带令牌以覆盖 raw_url 带 token 的分支）
	call(t, e, http.MethodPost, "/api/auth/register", `{"username":"u1","password":"`+pwdHash("p")+`"}`, "")
	_, resp := call(t, e, http.MethodPost, "/api/auth/login/hash", `{"username":"u1","password":"`+pwdHash("p")+`"}`, "")
	data, _ := resp["data"].(map[string]any)
	token, _ := data["token"].(string)

	code, resp := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media","per_page":0}`, token)
	if code != 200 {
		t.Fatalf("列目录应成功，got code=%d, msg=%v", code, resp["message"])
	}
	content, _ := resp["data"].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("应列出 2 个条目，got %d", len(content))
	}

	// 取详情：能拿到 raw_url，且类型识别为视频
	code, resp = call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/a.mp4"}`, token)
	if code != 200 {
		t.Fatalf("取详情应成功，got code=%d, msg=%v", code, resp["message"])
	}
	detail, _ := resp["data"].(map[string]any)
	if detail["name"] != "a.mp4" {
		t.Errorf("name = %v, want a.mp4", detail["name"])
	}
	if detail["type"] != float64(models.MediaVideo) {
		t.Errorf("type = %v, want %d（视频）", detail["type"], models.MediaVideo)
	}
	raw, _ := detail["raw_url"].(string)
	if !strings.HasPrefix(raw, "/d/media/a.mp4") {
		t.Errorf("raw_url = %q, 应以 /d/media/a.mp4 开头", raw)
	}

	// 取流：带令牌能下载到内容
	req := httptest.NewRequest(http.MethodGet, raw, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "video" {
		t.Errorf("取流失败: code=%d, body=%q", rec.Code, rec.Body.String())
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}
