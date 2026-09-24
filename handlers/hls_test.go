package handlers_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// hlsFixture 造一个含视频的本地存储，返回存储根目录。
func hlsFixture(t *testing.T, e *echo.Echo) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "v.mp4"), []byte("video-bytes"))
	addStorage(t, "/media", "Local", root)
	return root
}

// writePlaylist 按 mediaindex 的约定写入旁路清单与一个分片。
func writePlaylist(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, mediaindex.HLSDirName, name)
	mkdir(t, dir)
	writeFile(t, filepath.Join(dir, mediaindex.HLSPlaylistName),
		[]byte("#EXTM3U\n#EXTINF:2.0,\nseg00000.ts\n#EXT-X-ENDLIST\n"))
	writeFile(t, filepath.Join(dir, "seg00000.ts"), []byte("ts-bytes"))
}

// TestHLSPlaylistContentType 旁路清单与分片必须按标准类型发出。
//
// Go 内置 mime 表没有 .m3u8/.ts 映射（只有系统装了 apache 的 mime.types 才兜底），
// 不显式钉死的话，Safari 原生 HLS 会因清单不是 application/vnd.apple.mpegurl 而拒播。
func TestHLSPlaylistContentType(t *testing.T) {
	e := newApp(t)
	root := hlsFixture(t, e)
	writePlaylist(t, root, "v.mp4")

	for _, tc := range []struct{ path, want string }{
		{"/d/media/.hls/v.mp4/index.m3u8", "application/vnd.apple.mpegurl"},
		{"/d/media/.hls/v.mp4/seg00000.ts", "video/mp2t"},
	} {
		rec := serveRaw(t, e, tc.path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s 应 200，got %d", tc.path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); ct != tc.want {
			t.Errorf("%s Content-Type = %q, want %q", tc.path, ct, tc.want)
		}
	}
}

// TestFsGetExposesHLSURL 有旁路清单时 /fs/get 要带上 hls_url，没有就不带。
func TestFsGetExposesHLSURL(t *testing.T) {
	e := newApp(t)
	root := hlsFixture(t, e)

	code, resp := call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/v.mp4"}`, "")
	if code != 200 {
		t.Fatalf("取详情失败: code=%d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if v, _ := d["hls_url"].(string); v != "" {
		t.Errorf("还没有清单时不该有 hls_url，got %q", v)
	}

	writePlaylist(t, root, "v.mp4")

	_, resp = call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/v.mp4"}`, "")
	d, _ = resp["data"].(map[string]any)
	got, _ := d["hls_url"].(string)
	if !strings.Contains(got, "/media/.hls/v.mp4/index.m3u8") {
		t.Errorf("hls_url = %q，应指向旁路清单", got)
	}

	// 清单/分片自身不该再被判为「有旁路清单的视频」，否则会递归指向 .hls/xxx/index.m3u8/.hls/...
	for _, p := range []string{"/media/.hls/v.mp4/index.m3u8", "/media/.hls/v.mp4/seg00000.ts"} {
		_, resp = call(t, e, http.MethodPost, "/api/fs/get", `{"path":"`+p+`"}`, "")
		d, _ = resp["data"].(map[string]any)
		if v, _ := d["hls_url"].(string); v != "" {
			t.Errorf("%s 自身不该再有 hls_url，got %q", p, v)
		}
	}
}

// TestFsHLSRejectsNonVideo 非视频不得触发切分（避免对目录/音频白跑一次 ffmpeg）。
func TestFsHLSRejectsNonVideo(t *testing.T) {
	e := newApp(t)
	root := hlsFixture(t, e)
	writeFile(t, filepath.Join(root, "a.mp3"), []byte("audio"))
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	for _, p := range []string{"/media/a.mp3", "/media"} {
		code, resp := call(t, e, http.MethodPost, "/api/fs/hls", `{"path":"`+p+`"}`, admin)
		if code == 200 {
			t.Errorf("%s 不该允许切分: %v", p, resp)
		}
	}
}

// TestFsHLSRequiresAdmin 切分是写操作，必须管理员；游客/未登录要挡住。
func TestFsHLSRequiresAdmin(t *testing.T) {
	e := newApp(t)
	hlsFixture(t, e)
	code, _ := call(t, e, http.MethodPost, "/api/fs/hls", `{"path":"/media/v.mp4"}`, "")
	if code == 200 {
		t.Errorf("未登录不该允许切分，got %d", code)
	}
}

// TestFsHLSGeneratesPlaylist 真跑一次 ffmpeg 切分（本机没有 ffmpeg 则跳过）。
func TestFsHLSGeneratesPlaylist(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机未安装 ffmpeg，跳过切分集成测试")
	}
	e := newApp(t)
	root := t.TempDir()
	src := filepath.Join(root, "v.mp4")
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=10", "-t", "1.2",
		"-pix_fmt", "yuv420p", src).CombinedOutput()
	if err != nil {
		t.Fatalf("生成测试视频失败: %v\n%s", err, out)
	}
	addStorage(t, "/media", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/hls", `{"path":"/media/v.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("切分失败: code=%d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if got, _ := d["playlist"].(string); !strings.Contains(got, "/media/.hls/v.mp4/index.m3u8") {
		t.Errorf("playlist = %q，应指向约定位置", got)
	}
	if skipped, _ := d["skipped"].(bool); skipped {
		t.Error("首次切分不该是 skipped")
	}

	// 产物真的落在约定的隐藏目录里：清单 + 至少一个分片
	dir := filepath.Join(root, mediaindex.HLSDirName, "v.mp4")
	if _, err := os.Stat(filepath.Join(dir, mediaindex.HLSPlaylistName)); err != nil {
		t.Errorf("清单未生成: %v", err)
	}
	segs, _ := filepath.Glob(filepath.Join(dir, "seg*.ts"))
	if len(segs) == 0 {
		t.Error("没有生成任何分片")
	}

	// 清单必须能被取流，且带着正确的类型（前端据此播放）
	rec := serveRaw(t, e, "/d/media/.hls/v.mp4/index.m3u8", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("清单取流失败: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Error("清单内容不像 m3u8")
	}

	// 已有清单 → 默认跳过；force=true 才重切
	_, resp = call(t, e, http.MethodPost, "/api/fs/hls", `{"path":"/media/v.mp4"}`, admin)
	d, _ = resp["data"].(map[string]any)
	if skipped, _ := d["skipped"].(bool); !skipped {
		t.Error("已有清单时二次切分应 skipped=true")
	}
}
