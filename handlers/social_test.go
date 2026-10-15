package handlers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	web "github.com/azhai/mocca/lib/web"
	"github.com/azhai/mocca/models"
)

// socialFixture 造一个「有索引的视频 + 2 条弹幕 + 1 条评论 + 1 条回复」的环境。
// 返回管理员令牌、普通用户令牌、媒体路径，以及那条顶层评论的 ID（回复挂它上面）。
func socialFixture(t *testing.T, e *web.Echo) (admin, fan, filePath, topID string) {
	t.Helper()
	_, filePath = danmakuFixtures(t)
	admin = makeUser(t, e, "root", "p", models.RoleAdmin)
	fan = makeUser(t, e, "fan", "p", models.RoleGeneral)

	for _, off := range []string{"1000", "2000"} {
		body := fmt.Sprintf(`{"path":%q,"offset":%s,"content":"弹幕 %s"}`, filePath, off, off)
		if code, r := call(t, e, http.MethodPost, "/api/danmaku", body, fan); code != 200 {
			t.Fatalf("发弹幕失败: code=%d msg=%v", code, r["message"])
		}
	}
	code, r := call(t, e, http.MethodPost, "/api/comments",
		`{"path":"`+filePath+`","content":"一句评论"}`, fan)
	if code != 200 {
		t.Fatalf("发评论失败: code=%d msg=%v", code, r["message"])
	}
	data, _ := r["data"].(map[string]any)
	topID, _ = data["id"].(string)
	if topID == "" {
		t.Fatalf("评论响应里没有 id: %v", r["data"])
	}
	if code, rr := call(t, e, http.MethodPost, "/api/comments",
		`{"path":"`+filePath+`","content":"回复一句","parent_id":"`+topID+`"}`, admin); code != 200 {
		t.Fatalf("发回复失败: code=%d msg=%v", code, rr["message"])
	}
	return admin, fan, filePath, topID
}

// socialOf 取列表条目上的 social 计数（name -> {danmaku, comments}），顺带回原始响应体。
func socialOf(t *testing.T, e *web.Echo, body string) (map[string][2]int, string) {
	t.Helper()
	code, resp, raw := callRaw(t, e, http.MethodPost, "/api/fs/list", body, "")
	if code != 200 {
		t.Fatalf("列目录失败 body=%s code=%d msg=%v", body, code, resp["message"])
	}
	out := map[string][2]int{}
	data, _ := resp["data"].(map[string]any)
	content, _ := data["content"].([]any)
	for _, it := range content {
		row, ok := it.(map[string]any)
		if !ok {
			continue
		}
		s, ok := row["social"].(map[string]any)
		if !ok {
			continue
		}
		d, _ := s["danmaku"].(float64)
		c, _ := s["comments"].(float64)
		out[row["name"].(string)] = [2]int{int(d), int(c)}
	}
	return out, raw
}

// TestFsSocialPanel 后台一次读取拿全：条数 + 弹幕列表（带时间点）+ 评论串（回复挂在父评论下）。
func TestFsSocialPanel(t *testing.T) {
	e := newApp(t)
	admin, fan, filePath, topID := socialFixture(t, e)

	code, resp := call(t, e, http.MethodPost, "/api/fs/social", `{"path":"`+filePath+`"}`, admin)
	if code != 200 {
		t.Fatalf("取弹幕/评论失败: code=%d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	counts, _ := d["counts"].(map[string]any)
	if got := int(counts["danmaku"].(float64)); got != 2 {
		t.Errorf("弹幕条数 = %d, want 2", got)
	}
	// 评论计数含回复：顶层 1 + 回复 1
	if got := int(counts["comments"].(float64)); got != 2 {
		t.Errorf("评论条数 = %d, want 2（顶层 + 回复）", got)
	}
	dm, _ := d["danmaku"].([]any)
	if len(dm) != 2 {
		t.Fatalf("弹幕列表长度 = %d, want 2", len(dm))
	}
	first, _ := dm[0].(map[string]any)
	if int(first["offset"].(float64)) != 1000 {
		t.Errorf("第一条弹幕时间点 = %v, want 1000（审核要看的是原序）", first["offset"])
	}
	if first["user_name"] != "fan" {
		t.Errorf("作者 = %v, want fan（昵称快照）", first["user_name"])
	}
	threads, _ := d["comments"].([]any)
	if len(threads) != 1 {
		t.Fatalf("顶层评论数 = %d, want 1", len(threads))
	}
	th, _ := threads[0].(map[string]any)
	if th["id"] != topID {
		t.Errorf("顶层评论 ID = %v, want %v", th["id"], topID)
	}
	if reps, _ := th["replies"].([]any); len(reps) != 1 {
		t.Errorf("回复数 = %v, want 1（回复必须嵌在父评论下，不能浮在外面）", th["replies"])
	}

	// 非管理员进不了后台接口（admin 组）
	if code, _ := call(t, e, http.MethodPost, "/api/fs/social", `{"path":"`+filePath+`"}`, fan); code != 403 {
		t.Errorf("普通用户取审核数据 code = %d, want 403", code)
	}
}

// TestFsListSocialProbe 列表的弹幕计数：默认不探，探了也只给非零条目，分页路径同样要填。
//
// 后台的行内徽标依赖这三条语义，与 hls 探测同一套口径（见 ADR 0004）。
func TestFsListSocialProbe(t *testing.T) {
	e := newApp(t)
	socialFixture(t, e)

	// 1) 不带开关：响应里一个 social 键都不该出现（APP 不付这份读成本）
	_, raw := socialOf(t, e, `{"path":"/m"}`)
	if strings.Contains(raw, `"social"`) {
		t.Errorf("不带 social 开关时响应里出现了 social: %s", raw)
	}

	// 2) 带开关：s.mp4 上给出 2 弹幕 / 2 评论
	got, _ := socialOf(t, e, `{"path":"/m","social":true}`)
	if c := got["s.mp4"]; c != [2]int{2, 2} {
		t.Errorf("s.mp4 计数 = %v, want [2 2]", c)
	}

	// 3) 分页路径（网格用的那条实现）同样要填，否则两种视图给出矛盾答案
	page, _ := socialOf(t, e, `{"path":"/m","social":true,"page":1,"per_page":1}`)
	if c := page["s.mp4"]; c != [2]int{2, 2} {
		t.Errorf("分页路径的计数 = %v, want [2 2]", c)
	}

	// 4) 没有索引的层拿不到指纹，就不该有计数可探（也不能报错）
	root := contentRoot(t)
	addStorage(t, "/noidx", "Local", root)
	writeTestFile(t, root, "x.mp4", "bytes")
	_, raw = socialOf(t, e, `{"path":"/noidx","social":true}`)
	if strings.Contains(raw, `"social"`) {
		t.Errorf("没有索引的目录不该出现 social 字段: %s", raw)
	}
}

// TestFsSocialClear 清空按类型走：只清弹幕时评论必须留着，两类都清完计数就该消失。
func TestFsSocialClear(t *testing.T) {
	e := newApp(t)
	admin, _, filePath, _ := socialFixture(t, e)

	// kind 缺失必须报错：漏字段就清两类是数据丢失的坑
	if code, _ := call(t, e, http.MethodPost, "/api/fs/social/clear", `{"path":"`+filePath+`"}`, admin); code != 400 {
		t.Errorf("不带 kind 清空 code = %d, want 400", code)
	}
	// 只清弹幕
	code, resp := call(t, e, http.MethodPost, "/api/fs/social/clear",
		`{"path":"`+filePath+`","kind":"danmaku"}`, admin)
	if code != 200 {
		t.Fatalf("清空弹幕失败: code=%d msg=%v", code, resp["message"])
	}
	if d, _ := resp["data"].(map[string]any); int(d["cleared"].(float64)) != 2 {
		t.Errorf("cleared = %v, want 2", resp["data"])
	}
	got, _ := socialOf(t, e, `{"path":"/m","social":true}`)
	if c := got["s.mp4"]; c != [2]int{0, 2} {
		t.Errorf("只清弹幕后计数 = %v, want [0 2]（评论被误删了）", c)
	}

	// 两类都清：文件删掉，条目上不再挂 social 字段（计数为 0 就不下发）
	if code, resp := call(t, e, http.MethodPost, "/api/fs/social/clear",
		`{"path":"`+filePath+`","kind":"all"}`, admin); code != 200 {
		t.Fatalf("全清失败: code=%d msg=%v", code, resp["message"])
	} else if d, _ := resp["data"].(map[string]any); int(d["cleared"].(float64)) != 2 {
		t.Errorf("全清 cleared = %v, want 2（只剩 2 条评论）", resp["data"])
	}
	_, raw := socialOf(t, e, `{"path":"/m","social":true}`)
	if strings.Contains(raw, `"social"`) {
		t.Errorf("清空后不该再下发 social 字段: %s", raw)
	}
	// 空目录再清一次：没有要清的东西就返回 0，不能凭空造出一个文件
	if code, resp := call(t, e, http.MethodPost, "/api/fs/social/clear",
		`{"path":"`+filePath+`","kind":"all"}`, admin); code != 200 {
		t.Fatalf("重复清空失败: code=%d msg=%v", code, resp["message"])
	} else if d, _ := resp["data"].(map[string]any); int(d["cleared"].(float64)) != 0 {
		t.Errorf("重复清空 cleared = %v, want 0", resp["data"])
	}
}

// TestAdminDeletesOthersEntry 后台的单条删除**复用** DELETE /api/comments：
// 管理员能删别人的，普通用户删别人的必须被拒 —— 这条放行是整个审核面板的前提。
func TestAdminDeletesOthersEntry(t *testing.T) {
	e := newApp(t)
	admin, _, filePath, topID := socialFixture(t, e)
	other := makeUser(t, e, "other", "p", models.RoleGeneral)

	// 普通用户删别人发的评论
	if code, _ := call(t, e, http.MethodDelete, "/api/comments",
		`{"path":"`+filePath+`","id":"`+topID+`"}`, other); code == 200 {
		t.Errorf("普通用户删别人的评论成功了，code=%d", code)
	}
	// 管理员删：成功，且列表里少一条
	if code, resp := call(t, e, http.MethodDelete, "/api/comments",
		`{"path":"`+filePath+`","id":"`+topID+`"}`, admin); code != 200 {
		t.Fatalf("管理员删评论失败: code=%d msg=%v", code, resp["message"])
	}
	_, d := call(t, e, http.MethodPost, "/api/fs/social", `{"path":"`+filePath+`"}`, admin)
	data, _ := d["data"].(map[string]any)
	counts, _ := data["counts"].(map[string]any)
	// 顶层评论被删后，它的回复不再展示（CommentThreads 丢掉孤儿回复），
	// 但记录本身还在文件里 —— 计数按记录算，所以评论仍是 1。
	if got := int(counts["danmaku"].(float64)); got != 2 {
		t.Errorf("删一条评论后弹幕计数 = %d, want 2（不该被牵连）", got)
	}
	if threads, _ := data["comments"].([]any); len(threads) != 0 {
		t.Errorf("被删的顶层评论还在列表里: %v", data["comments"])
	}
}
