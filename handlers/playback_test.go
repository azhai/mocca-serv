package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// mediaFixture 挂一个含视频与音频的本地存储。
func mediaFixture(t *testing.T, e *echo.Echo) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "v.mp4"), []byte("video-bytes"))
	writeFile(t, filepath.Join(root, "a.mp3"), []byte("audio-bytes"))
	addStorage(t, "/media", "Local", root)
}

// rawURLOf 走 /api/fs/get 取播放地址。token 为空即游客。
func rawURLOf(t *testing.T, e *echo.Echo, path, token string) string {
	t.Helper()
	code, resp := call(t, e, http.MethodPost, "/api/fs/get", `{"path":"`+path+`"}`, token)
	if code != 200 {
		t.Fatalf("取详情失败 %s: code=%d msg=%v", path, code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	raw, _ := d["raw_url"].(string)
	if raw == "" {
		t.Fatalf("%s 未返回 raw_url", path)
	}
	return raw
}

// serveRaw 按播放器的方式请求该地址：**不带任何请求头**，凭证只能来自 URL 本身。
func serveRaw(t *testing.T, e *echo.Echo, raw string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, raw, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestGuestCanPlayMedia 未登录（游客）也必须能播放。
//
// 这是一条回归测试：原先 /d/*path 强制要求登录，而浏览不需要登录，
// 于是游客点任何视频/音频都拿到 401 的 JSON 信封，播放器一律报错。
func TestGuestCanPlayMedia(t *testing.T) {
	e := newApp(t)
	mediaFixture(t, e)

	for _, tc := range []struct{ path, want string }{
		{"/media/v.mp4", "video-bytes"},
		{"/media/a.mp3", "audio-bytes"},
	} {
		raw := rawURLOf(t, e, tc.path, "")
		rec := serveRaw(t, e, raw, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("游客取流 %s 应 200，got %d body=%q (raw_url=%q)", tc.path, rec.Code, rec.Body.String(), raw)
			continue
		}
		if rec.Body.String() != tc.want {
			t.Errorf("游客取流 %s 内容不符，got %q want %q", tc.path, rec.Body.String(), tc.want)
		}
		// 播放器要能认出媒体类型；回 application/json 说明又是错误信封
		if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "json") {
			t.Errorf("Content-Type = %q，不该是 JSON", ct)
		}
	}
}

// TestLoggedInCanPlayMedia 已登录时令牌经查询串生效（既有行为，防回归）。
func TestLoggedInCanPlayMedia(t *testing.T) {
	e := newApp(t)
	mediaFixture(t, e)
	tok := makeUser(t, e, "u1", "p", models.RoleGeneral)

	raw := rawURLOf(t, e, "/media/v.mp4", tok)
	if !strings.Contains(raw, "token=") {
		t.Errorf("已登录的 raw_url 应带 token，got %q", raw)
	}
	rec := serveRaw(t, e, raw, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "video-bytes" {
		t.Errorf("登录用户取流失败: code=%d body=%q", rec.Code, rec.Body.String())
	}
	// 令牌放在请求头同样可用（APP 的下载入口走 header）
	if rec = serveRaw(t, e, "/d/media/v.mp4", map[string]string{"Authorization": tok}); rec.Code != http.StatusOK {
		t.Errorf("令牌放请求头也应可播，got %d", rec.Code)
	}
}

// TestStreamGuestPolicyFollowsSetting 关掉「允许游客」后，取流必须真拒绝，
// 且回的是**真实 HTTP 401** —— 回 200 会让播放器把 JSON 当媒体数据解码。
func TestStreamGuestPolicyFollowsSetting(t *testing.T) {
	e := newApp(t)
	mediaFixture(t, e)

	// 默认允许游客
	if rec := serveRaw(t, e, "/d/media/v.mp4", nil); rec.Code != http.StatusOK {
		t.Fatalf("默认应允许游客取流，got %d", rec.Code)
	}

	if err := models.SetSettingBool(handlers.SettingAllowGuest, false); err != nil {
		t.Fatal(err)
	}
	rec := serveRaw(t, e, "/d/media/v.mp4", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("关闭游客后应回 HTTP 401，got %d body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "401") {
		t.Errorf("响应体应保留业务码 401，got %q", rec.Body.String())
	}
	// 取详情的入口也应同步拒绝（两端策略一致）
	if code, _ := call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/v.mp4"}`, ""); code == 200 {
		t.Error("关闭游客后 /fs/get 不该对游客放行")
	}

	// 登录用户不受该开关影响
	tok := makeUser(t, e, "u1", "p", models.RoleGeneral)
	if rec = serveRaw(t, e, "/d/media/v.mp4", map[string]string{"Authorization": tok}); rec.Code != http.StatusOK {
		t.Errorf("登录用户应不受游客开关影响，got %d", rec.Code)
	}
}

// TestStreamRejectsBadToken 令牌无效时不静默降级成游客，否则「登录失效」
// 会表现为「能浏览但播不了」，非常难查。
func TestStreamRejectsBadToken(t *testing.T) {
	e := newApp(t)
	mediaFixture(t, e)

	rec := serveRaw(t, e, "/d/media/v.mp4?token=not-a-real-jwt", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("坏令牌应回 HTTP 401，got %d body=%q", rec.Code, rec.Body.String())
	}
}

// TestStreamSupportsRange 播放器拖动进度条依赖 Range 请求。
func TestStreamSupportsRange(t *testing.T) {
	e := newApp(t)
	mediaFixture(t, e)

	rec := serveRaw(t, e, "/d/media/v.mp4", map[string]string{"Range": "bytes=0-4"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Range 请求应回 206，got %d", rec.Code)
	}
	if rec.Body.String() != "video" {
		t.Errorf("分段内容不符，got %q want %q", rec.Body.String(), "video")
	}
}
