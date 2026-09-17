package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// avatarTopColor 从头像 SVG 里取出渐变上段的颜色。
var avatarTopColor = regexp.MustCompile(`stop offset="0" stop-color="(#[0-9A-F]{6})"`)

// pwdHash 把明文口令转成客户端会提交的静态哈希。
//
// 真实客户端（APP 与内嵌后台）提交口令前一律先做这一步，服务端也只接受这种。
// 测试里直接用明文会掩盖约定错误 —— 后台的用户表单就曾误传明文、把账号静默写坏
// （2026-09-17 事故），而当时所有测试都用明文，没有任何一条能发现它。
func pwdHash(plain string) string { return models.StaticHash(plain) }

// makeUser 直接建账号并返回登录后的令牌；role 用 models.RoleAdmin 可造管理员。
// 口令与真实客户端一致：库里存 bcrypt(StaticHash(明文))，登录发 StaticHash(明文)。
func makeUser(t *testing.T, e *echo.Echo, name, pw string, role int) string {
	t.Helper()
	pwd := pwdHash(pw)
	u := &models.User{Username: name, Role: role}
	if err := u.SetPassword(pwd); err != nil {
		t.Fatalf("SetPassword 失败: %v", err)
	}
	if err := models.CreateUser(u); err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	_, resp := call(t, e, http.MethodPost, "/api/auth/login/hash",
		`{"username":"`+name+`","password":"`+pwd+`"}`, "")
	data, _ := resp["data"].(map[string]any)
	tok, _ := data["token"].(string)
	if tok == "" {
		t.Fatalf("登录 %s 未拿到令牌: %v", name, resp)
	}
	return tok
}

func TestStorageRequiresAdmin(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()

	// 普通用户：403
	userTok := makeUser(t, e, "u", "p", models.RoleGeneral)
	if code, _ := call(t, e, http.MethodPost, "/api/storage/create",
		`{"mount_path":"/m","addition":"{}"}`, userTok); code != 403 {
		t.Errorf("普通用户创建存储应返回 403，got %d", code)
	}
	// 未登录：401
	if code, _ := call(t, e, http.MethodPost, "/api/storage/create",
		`{"mount_path":"/m"}`, ""); code != 401 {
		t.Errorf("未登录应返回 401，got %d", code)
	}

	// 管理员：创建 → 列表 → 删除
	adminTok := makeUser(t, e, "root", "p", models.RoleAdmin)
	body := `{"mount_path":"/media","driver":"Local","addition":"{\"root_folder_path\":\"` + root + `\"}"}`
	if code, resp := call(t, e, http.MethodPost, "/api/storage/create", body, adminTok); code != 200 {
		t.Fatalf("管理员创建存储应成功，got %d, msg=%v", code, resp["message"])
	}
	code, resp := call(t, e, http.MethodGet, "/api/storage/list", "", adminTok)
	if code != 200 {
		t.Fatalf("列出存储应成功，got %d", code)
	}
	list, _ := resp["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("应有 1 个挂载点，got %d", len(list))
	}
	id := list[0].(map[string]any)["id"].(float64)

	// 有了挂载点，列目录就能用
	if code, _ := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media"}`, adminTok); code != 200 {
		t.Errorf("挂载后列目录应成功，got %d", code)
	}

	if code, _ := call(t, e, http.MethodPost, "/api/storage/delete",
		`{"id":`+strconv.Itoa(int(id))+`}`, adminTok); code != 200 {
		t.Errorf("删除存储应成功，got %d", code)
	}
	if code, resp := call(t, e, http.MethodGet, "/api/storage/list", "", adminTok); code == 200 {
		if list, _ := resp["data"].([]any); len(list) != 0 {
			t.Errorf("删除后应为空，got %d", len(list))
		}
	}
}

func TestFavoriteEndpoints(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "fav", "p", models.RoleGeneral)

	if code, _ := call(t, e, http.MethodPost, "/api/favorites",
		`{"path":"/m/a.mp4","name":"a.mp4","kind":`+itoa(models.MediaVideo)+`}`, tok); code != 200 {
		t.Fatalf("收藏应成功，got %d", code)
	}
	// 重复收藏不产生第二条
	call(t, e, http.MethodPost, "/api/favorites", `{"path":"/m/a.mp4","kind":`+itoa(models.MediaVideo)+`}`, tok)

	code, resp := call(t, e, http.MethodGet, "/api/favorites", "", tok)
	if code != 200 {
		t.Fatalf("列出收藏应成功，got %d", code)
	}
	items, _ := resp["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("收藏应去重，got %d 条", len(items))
	}

	if code, _ := call(t, e, http.MethodDelete, "/api/favorites",
		`{"path":"/m/a.mp4"}`, tok); code != 200 {
		t.Errorf("取消收藏应成功，got %d", code)
	}
	if _, resp := call(t, e, http.MethodGet, "/api/favorites", "", tok); len(resp["data"].([]any)) != 0 {
		t.Errorf("取消后应为空，got %v", resp["data"])
	}

	// 未登录不能收藏
	if code, _ := call(t, e, http.MethodPost, "/api/favorites", `{"path":"/x"}`, ""); code != 401 {
		t.Errorf("未登录收藏应返回 401，got %d", code)
	}
}

func TestCommentAndDanmakuEndpoints(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "cm", "p", models.RoleGeneral)
	const path = "/m/a.mp4"

	// 评论：长文本放行且时刻归零
	long := strings.Repeat("评", 200)
	if code, _ := call(t, e, http.MethodPost, "/api/comments",
		`{"path":"`+path+`","type":0,"offset":9999,"content":"`+long+`"}`, tok); code != 200 {
		t.Fatalf("发评论应成功，got %d", code)
	}
	// 弹幕：50 字放行
	if code, _ := call(t, e, http.MethodPost, "/api/comments",
		`{"path":"`+path+`","type":1,"offset":30000,"content":"`+strings.Repeat("弹", 50)+`"}`, tok); code != 200 {
		t.Fatalf("50 字弹幕应成功，got %d", code)
	}
	// 弹幕：51 字被拒
	if code, _ := call(t, e, http.MethodPost, "/api/comments",
		`{"path":"`+path+`","type":1,"offset":0,"content":"`+strings.Repeat("弹", 51)+`"}`, tok); code != 400 {
		t.Errorf("51 字弹幕应返回 400，got %d", code)
	}

	_, resp := call(t, e, http.MethodGet, "/api/comments?path="+path, "", "")
	if comments, _ := resp["data"].([]any); len(comments) != 1 {
		t.Errorf("评论应有 1 条，got %d", len(comments))
	} else if c := comments[0].(map[string]any); c["offset"] != float64(0) {
		t.Errorf("评论时刻应为 0，got %v", c["offset"])
	}

	_, resp = call(t, e, http.MethodGet, "/api/danmaku?path="+path, "", "")
	if dm, _ := resp["data"].([]any); len(dm) != 1 {
		t.Errorf("弹幕应有 1 条，got %d", len(dm))
	} else if d := dm[0].(map[string]any); d["offset"] != float64(30000) {
		t.Errorf("弹幕时刻应为 30000，got %v", d["offset"])
	}

	// 缺 path 直接 400
	if code, _ := call(t, e, http.MethodGet, "/api/comments", "", ""); code != 400 {
		t.Errorf("缺少 path 应返回 400，got %d", code)
	}
}

func TestMetaEndpoints(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "meta", "p", models.RoleAdmin)

	body := `{"path":"/m/a.mp4","kind":` + itoa(models.MediaVideo) + `,"size":123,"title":"标题","duration":60000,` +
		`"cover":"` + models.CoverRelPath("a.jpg") + `","description":"简介","authors":["甲","乙"]}`
	if code, _ := call(t, e, http.MethodPost, "/api/meta/save", body, tok); code != 200 {
		t.Fatalf("保存元数据应成功，got %d", code)
	}
	code, resp := call(t, e, http.MethodGet, "/api/meta?path=/m/a.mp4", "", "")
	if code != 200 {
		t.Fatalf("读取元数据应成功，got %d", code)
	}
	data, _ := resp["data"].(map[string]any)
	meta, _ := data["meta"].(map[string]any)
	if meta["duration"] != float64(60000) {
		t.Errorf("duration = %v, want 60000", meta["duration"])
	}
	authors, _ := data["authors"].([]any)
	if len(authors) != 2 {
		t.Errorf("作者应有 2 个，got %v", authors)
	}

	// 非法类型被拒
	if code, _ := call(t, e, http.MethodPost, "/api/meta/save",
		`{"path":"/x","kind":99}`, tok); code != 400 {
		t.Errorf("非法媒体类型应返回 400，got %d", code)
	}
}

func TestAvatarServesSVG(t *testing.T) {
	e := newApp(t)

	req := httptest.NewRequest(http.MethodGet, "/static/avatars/03.png", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("头像应返回 200，got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "svg") {
		t.Errorf("Content-Type = %q, want svg", ct)
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Errorf("应返回 SVG，got %q", rec.Body.String())
	}

	// 非预设 key：404（走信封，不是图片）
	req = httptest.NewRequest(http.MethodGet, "/static/avatars/hack.png", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "404") {
		t.Errorf("非法头像应返回 404 信封，got %q", rec.Body.String())
	}
}

// TestAvatarPresetsAreDistinctAndComplete 12 个预设头像必须都取得到、
// 各自颜色互不相同、且画的是自己的编号。
//
// 配色是手挑的：写重一个色值会让两个头像长得一模一样，肉眼很难发现，
// 所以这里逐个取回、按颜色去重比对。
func TestAvatarPresetsAreDistinctAndComplete(t *testing.T) {
	e := newApp(t)
	colors := map[string]string{}

	for _, key := range models.AvatarPresets {
		req := httptest.NewRequest(http.MethodGet, "/static/avatars/"+key+".png", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("预设头像 %s 应返回 200，got %d", key, rec.Code)
		}
		// 头像会被嵌进页面，声明不嗅探
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("头像 %s 的 X-Content-Type-Options = %q，want nosniff", key, got)
		}

		body := rec.Body.String()
		if !strings.Contains(body, ">"+key+"</text>") {
			t.Errorf("头像 %s 的 SVG 里没有自己的编号: %s", key, body)
		}
		if strings.Count(body, "<stop ") != 2 || !strings.Contains(body, "linearGradient") {
			t.Errorf("头像 %s 应是两段渐变: %s", key, body)
		}

		m := avatarTopColor.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("头像 %s 的底色解析失败: %s", key, body)
		}
		if prev, dup := colors[m[1]]; dup {
			t.Errorf("头像 %s 与 %s 撞色 %s", key, prev, m[1])
		}
		colors[m[1]] = key
	}

	if len(colors) != len(models.AvatarPresets) {
		t.Errorf("不同颜色数 %d 与预设数 %d 不一致", len(colors), len(models.AvatarPresets))
	}
}
