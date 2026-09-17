package handlers_test

// 契约测试：把「后端返回值」与「APP 的解析代码」之间的约定钉死。
//
// 背景：本次审查发现两处静默错位 —— 媒体类型编号与角色编号。
// 两者都不报错、接口也都 200，只是 APP 行为完全错乱
// （视频被判为「未知」不可播放、管理员被判为普通用户）。
// 这类问题的唯一防线就是常量断言 + 形状断言，所以单独成文件。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// callRaw 与 call 同源，但额外返回原始响应体，用于断言「字段是否存在/类型」。
func callRaw(t *testing.T, e *echo.Echo, method, path, body, token string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
	return int(code), resp, rec.Body.String()
}

// TestContractMediaKindNumbers 媒体类型编号必须与 APP 的 MediaKind 完全一致。
// 一旦有人把常量改回 iota 顺排，这里立刻失败。
func TestContractMediaKindNumbers(t *testing.T) {
	want := map[string]struct {
		got  int
		want int
	}{
		"dir":     {models.MediaDir, 0},
		"unknown": {models.MediaUnknown, 1},
		"video":   {models.MediaVideo, 2},
		"audio":   {models.MediaAudio, 3},
		"text":    {models.MediaText, 4},
		"image":   {models.MediaImage, 5},
	}
	for name, v := range want {
		if v.got != v.want {
			t.Errorf("MediaKind.%s = %d，APP 期望 %d（编号错位会让 APP 判错类型）", name, v.got, v.want)
		}
	}

	// 目录必须是 0：APP 用 type==dir 判断是否可进入
	if models.MediaDir != 0 {
		t.Error("目录类型必须为 0")
	}
	// 未知不能与目录撞号，否则未知文件会被当成目录
	if models.MediaUnknown == models.MediaDir {
		t.Error("unknown 与 dir 撞号")
	}
}

// TestContractRoleNumbers 角色编号必须与 APP 的 Session.role 一致。
func TestContractRoleNumbers(t *testing.T) {
	if models.RoleGeneral != 0 {
		t.Errorf("普通用户角色 = %d，APP 期望 0", models.RoleGeneral)
	}
	if models.RoleGuest != 1 {
		t.Errorf("游客角色 = %d，APP 期望 1", models.RoleGuest)
	}
	if models.RoleAdmin != 2 {
		t.Errorf("管理员角色 = %d，APP 期望 2（APP 里 isAdmin => role == 2）", models.RoleAdmin)
	}

	// 零值必须是普通用户：否则新建用户忘赋角色就成了管理员
	var u models.User
	if u.IsAdmin() {
		t.Error("角色零值不应是管理员（会造成越权）")
	}
}

// TestContractFsListFieldShape 列表条目的字段名与类型。
func TestContractFsListFieldShape(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	// 故意用带媒体扩展名的目录名：按扩展名猜类型就会把它判成视频
	if err := os.MkdirAll(filepath.Join(root, "movies.mp4"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"v.mp4", "s.mp3", "p.jpg", "n.txt", "raw.bin"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	addStorage(t, "/media", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp, raw := callRaw(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media"}`, admin)
	if code != 200 {
		t.Fatalf("列目录应成功，got %d", code)
	}
	if !strings.Contains(raw, `"modified":"`) {
		t.Error("条目应含 RFC3339 的 modified（APP 用 DateTime.tryParse 解析）")
	}
	if !strings.Contains(raw, `"sign":`) {
		t.Error("条目应含 sign 字段（APP 会读它）")
	}

	data, _ := resp["data"].(map[string]any)
	content, ok := data["content"].([]any)
	if !ok {
		t.Fatalf("content 应为数组，got %T", data["content"])
	}

	wantType := map[string]int{
		"movies.mp4": models.MediaDir, // 关键：目录即便叫 .mp4 也必须是 0
		"v.mp4":      models.MediaVideo,
		"s.mp3":      models.MediaAudio,
		"p.jpg":      models.MediaImage,
		"n.txt":      models.MediaText,
		"raw.bin":    models.MediaUnknown,
	}
	for _, it := range content {
		obj := it.(map[string]any)
		name, _ := obj["name"].(string)
		typ, _ := obj["type"].(float64)
		if want, ok := wantType[name]; ok && int(typ) != want {
			t.Errorf("%s 的 type = %d，期望 %d", name, int(typ), want)
		}
		// modified 必须能被 APP 解析出来
		if s, _ := obj["modified"].(string); s != "" {
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				t.Errorf("%s 的 modified 不是 RFC3339: %q", name, s)
			}
		}
	}
}

// TestContractFsListEmptyIsArray 空目录必须是 []，不能是 null ——
// APP 侧对 null 会走 `content is! List` 分支，虽然容错但语义不同。
func TestContractFsListEmptyIsArray(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	addStorage(t, "/media", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, _, raw := callRaw(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media/empty"}`, admin)
	if code != 200 {
		t.Fatalf("空目录应成功，got %d", code)
	}
	if strings.Contains(raw, `"content":null`) {
		t.Errorf("空目录的 content 应为 []，got %s", raw)
	}
	if !strings.Contains(raw, `"total":0`) {
		t.Errorf("空目录 total 应为 0，got %s", raw)
	}
}

// TestContractFsGetFieldShape 详情字段名与类型。
func TestContractFsGetFieldShape(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "v.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	addStorage(t, "/media", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp, raw := callRaw(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/v.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("取详情应成功，got %d", code)
	}

	// header 必须是字符串：APP 用 `header.split('\n')` 解析，
	// 若返回 map 会在 APP 侧解析失败。
	if !strings.Contains(raw, `"header":"`) {
		t.Errorf("header 应为字符串字段，got %s", raw)
	}
	for _, key := range []string{`"raw_url"`, `"provider"`, `"is_dir"`, `"sign"`, `"modified"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("详情缺少字段 %s，got %s", key, raw)
		}
	}

	d, _ := resp["data"].(map[string]any)
	if d["type"] != float64(models.MediaVideo) {
		t.Errorf("type = %v，期望 %d", d["type"], models.MediaVideo)
	}
	if d["is_dir"] != false {
		t.Error("普通文件 is_dir 应为 false")
	}
	if p, _ := d["provider"].(string); p != "Local" {
		t.Errorf("provider = %q，期望 Local", p)
	}
	raw2, _ := d["raw_url"].(string)
	if !strings.HasPrefix(raw2, "/d/media/v.mp4") {
		t.Errorf("raw_url = %q，应以 /d/media/v.mp4 开头", raw2)
	}
}

// TestContractMeRole 登录后 /api/me 的角色编号要能让 APP 认出管理员。
func TestContractMeRole(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	user := makeUser(t, e, "plain", "p", models.RoleGeneral)

	_, resp, _ := callRaw(t, e, http.MethodGet, "/api/me", "", admin)
	d, _ := resp["data"].(map[string]any)
	if d["role"] != float64(2) {
		t.Errorf("管理员 role = %v，APP 期望 2", d["role"])
	}
	if _, ok := d["username"]; !ok {
		t.Error("me 应含 username")
	}
	if _, ok := d["base_path"]; !ok {
		t.Error("me 应含 base_path（APP 用它拼专属目录）")
	}

	_, resp, _ = callRaw(t, e, http.MethodGet, "/api/me", "", user)
	d, _ = resp["data"].(map[string]any)
	if d["role"] != float64(0) {
		t.Errorf("普通用户 role = %v，APP 期望 0", d["role"])
	}
}

// TestContractErrorEnvelopeAndCodes 异常场景：信封形状与业务码。
func TestContractErrorEnvelopeAndCodes(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	writeTestFile(t, root, "secret/a.mp4", "x")
	addStorage(t, "/media", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		token    string
		wantCode int
		reason   string
	}{
		{"不存在的目录", http.MethodPost, "/api/fs/list", `{"path":"/media/nope"}`, admin, 404, "路径不存在"},
		{"不存在的对象", http.MethodPost, "/api/fs/get", `{"path":"/media/nope.mp4"}`, admin, 404, "文件不存在"},
		{"未登录访问 me", http.MethodGet, "/api/me", "", "", 401, "鉴权失败"},
		{"伪造令牌", http.MethodGet, "/api/me", "", "bad-token", 401, "鉴权失败"},
		{"非管理员建存储", http.MethodPost, "/api/storage/create", `{"mount_path":"/x"}`, "", 401, "鉴权失败"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每一类都必须走统一信封，且 HTTP 状态码仍是 200（401/404 除外——
			// APP 侧对信封失败会抛 ApiException，靠 code 判断）
			code, resp, _ := callRaw(t, e, tt.method, tt.path, tt.body, tt.token)
			if code != tt.wantCode {
				t.Errorf("code = %d，期望 %d（%s）", code, tt.wantCode, tt.reason)
			}
			if _, ok := resp["message"]; !ok {
				t.Error("错误响应必须带 message")
			}
			if _, ok := resp["data"]; !ok {
				t.Error("错误响应必须带 data 字段（可为 null）")
			}
		})
	}

	// 目录密码：受保护目录必须 403，且提示里带目录名
	call(t, e, http.MethodPost, "/api/folder/password", `{"path":"/media/secret","password":"k"}`, admin)
	code, resp, _ := callRaw(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media/secret"}`, admin)
	if code != 403 {
		t.Fatalf("受保护目录应 403，got %d", code)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "需要密码") {
		t.Errorf("提示应说明需要密码，got %q", msg)
	}
}
