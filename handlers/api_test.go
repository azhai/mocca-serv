package handlers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/azhai/mocca/mediaindex"
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

// TestCommentAndDanmakuEndpoints 弹幕与评论接口。
//
// 数据**不落库**，落在设备侧的 .mocca（见 mediaindex/danmaku.go）；评论就是「时间点为 0 的
// 弹幕」，与弹幕共用一份存储。这条同时钉住契约：
//   - 发弹幕、发评论是两个端点；
//   - 弹幕支持按时间轴取窗口（from/to）；
//   - 评论返回「顶层 + 回复」，回复不再浮在顶层；
//   - 作者昵称与头像在**发送那一刻**就快照进记录（文件存储没法 join 用户表）。
func TestCommentAndDanmakuEndpoints(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/m", "Local", root)
	writeTestFile(t, root, "a.mp4", "video-data")
	// 弹幕按 sha1 锚定，得先从 .index.jsonl 拿到它（与海报/简介同一套寻址）
	sha := strings.Repeat("ab", 20)
	writeTestFile(t, root, ".index.jsonl",
		`{"name":"a.mp4","size_kb":1,"modified":"2026-01-01T00:00:00Z","sha1":"`+sha+`","is_new":0}`+"\n")

	tok := makeUser(t, e, "cm", "p", models.RoleGeneral)
	const path = "/m/a.mp4"

	// 弹幕：50 字放行、51 字拒绝
	if code, resp := call(t, e, http.MethodPost, "/api/danmaku",
		`{"path":"`+path+`","offset":30000,"content":"`+strings.Repeat("弹", 50)+`"}`, tok); code != 200 {
		t.Fatalf("50 字弹幕应成功，got %d msg=%v", code, resp["message"])
	}
	if code, _ := call(t, e, http.MethodPost, "/api/danmaku",
		`{"path":"`+path+`","offset":0,"content":"`+strings.Repeat("弹", 51)+`"}`, tok); code != 400 {
		t.Errorf("51 字弹幕应返回 400，got %d", code)
	}

	// 评论：长文本放行；再回一条（回复只支持一层）
	code, resp := call(t, e, http.MethodPost, "/api/comments",
		`{"path":"`+path+`","content":"`+strings.Repeat("评", 200)+`"}`, tok)
	if code != 200 {
		t.Fatalf("发评论应成功，got %d msg=%v", code, resp["message"])
	}
	top, _ := resp["data"].(map[string]any)
	topID, _ := top["id"].(string)
	if topID == "" {
		t.Fatalf("评论应返回带 id 的记录: %v", resp["data"])
	}
	if code, resp := call(t, e, http.MethodPost, "/api/comments",
		`{"path":"`+path+`","content":"回复","parent_id":"`+topID+`"}`, tok); code != 200 {
		t.Fatalf("回复应成功，got %d msg=%v", code, resp["message"])
	}

	// 评论列表：一条顶层 + 它的回复
	_, resp = call(t, e, http.MethodGet, "/api/comments?path="+path, "", "")
	threads, _ := resp["data"].([]any)
	if len(threads) != 1 {
		t.Fatalf("应只有 1 条顶层评论（回复不该浮出来），got %d: %v", len(threads), resp["data"])
	}
	t0, _ := threads[0].(map[string]any)
	if t0["offset"] != float64(0) {
		t.Errorf("评论时刻应为 0，got %v", t0["offset"])
	}
	if reps, _ := t0["replies"].([]any); len(reps) != 1 {
		t.Errorf("顶层评论应带 1 条回复，got %v", t0["replies"])
	}
	// 作者快照：昵称 + 头像（未选头像时给默认 01）
	if t0["user_name"] != "cm" || t0["user_avatar"] != "01" {
		t.Errorf("作者快照不对: name=%v avatar=%v", t0["user_name"], t0["user_avatar"])
	}
	if t0["user_id"] == float64(0) {
		t.Error("用户标识不该为空")
	}

	// 弹幕列表 + 按时间轴取窗口
	_, resp = call(t, e, http.MethodGet, "/api/danmaku?path="+path, "", "")
	dm, _ := resp["data"].([]any)
	if len(dm) != 1 {
		t.Fatalf("弹幕应有 1 条，got %d", len(dm))
	}
	if d, _ := dm[0].(map[string]any); d["offset"] != float64(30000) {
		t.Errorf("弹幕时刻应为 30000，got %v", d["offset"])
	}
	for _, tc := range []struct {
		q    string
		want int
	}{
		{"&from=30000&to=30000", 1}, // 端点包含在内
		{"&from=30001", 0},
		{"&to=29999", 0},
		{"&from=0&to=60000", 1},
	} {
		_, resp = call(t, e, http.MethodGet, "/api/danmaku?path="+path+tc.q, "", "")
		got, _ := resp["data"].([]any)
		if len(got) != tc.want {
			t.Errorf("弹幕窗口 %s 应有 %d 条，got %d", tc.q, tc.want, len(got))
		}
	}

	// 落盘位置：设备侧的 .mocca 下，按 sha1 分目录（不是数据库）
	rel, err := mediaindex.DanmakuRel(sha)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".mocca", filepath.FromSlash(rel))
	if _, err := os.Stat(p); err != nil {
		t.Errorf("弹幕应落在 %s: %v", p, err)
	}

	// 越权：别人删不掉我的；我自己可以删
	other := makeUser(t, e, "other", "p", models.RoleGeneral)
	if code, _ := call(t, e, http.MethodDelete, "/api/comments",
		`{"path":"`+path+`","id":"`+topID+`"}`, other); code != 400 {
		t.Errorf("删别人的评论应被拒，got %d", code)
	}
	if code, _ := call(t, e, http.MethodDelete, "/api/comments",
		`{"path":"`+path+`","id":"`+topID+`"}`, tok); code != 200 {
		t.Errorf("删自己的评论应成功，got %d", code)
	}

	// 未登录不能发；缺 path 一律 400
	if code, _ := call(t, e, http.MethodPost, "/api/danmaku",
		`{"path":"`+path+`","offset":1,"content":"x"}`, ""); code != 401 {
		t.Errorf("未登录发弹幕应返回 401，got %d", code)
	}
	if code, _ := call(t, e, http.MethodGet, "/api/comments", "", ""); code != 400 {
		t.Errorf("缺少 path 应返回 400，got %d", code)
	}
	if code, _ := call(t, e, http.MethodGet, "/api/danmaku", "", ""); code != 400 {
		t.Errorf("缺少 path 应返回 400，got %d", code)
	}
}

// TestDanmakuNeedsIndex 没索引就拿不到内容指纹，弹幕没法锚定 —— 必须明确报错，
// 而不是静默写进一个空锚点（那样改名后必丢）。
func TestDanmakuNeedsIndex(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/m", "Local", root)
	writeTestFile(t, root, "b.mp4", "video-data") // 故意不写 .index.jsonl
	tok := makeUser(t, e, "noidx", "p", models.RoleGeneral)

	code, resp := call(t, e, http.MethodPost, "/api/danmaku",
		`{"path":"/m/b.mp4","offset":1,"content":"x"}`, tok)
	if code != 400 {
		t.Fatalf("无索引时应 400，got %d", code)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "索引") {
		t.Errorf("应提示先索引，got %q", msg)
	}
}

// TestLegacyMetaRoutesRemoved 守卫：旧 DB 媒体的 /api/meta、/api/meta/save 已随
// MediaMeta 移除而删除，命中必须不再是 200（避免有人在没改前端的情况下用旧接口）。
func TestLegacyMetaRoutesRemoved(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "meta", "p", models.RoleAdmin)

	// 用裸请求：`call` 助手把非 200 HTTP 当失败，而这里恰恰要断言「不再是 200」。
	check := func(method, target, body string) {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, rd)
		req.Header.Set("Authorization", tok)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s %s 应已随 MediaMeta 移除，got 200", method, target)
		}
	}
	check(http.MethodGet, "/api/meta?path=/m/a.mp4", "")
	check(http.MethodPost, "/api/meta/save", `{"path":"/m/a.mp4","kind":2}`)
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
