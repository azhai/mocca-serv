package handlers_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	web "github.com/azhai/mocca/lib/web"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
)

// hlsFixture 造一个含视频的本地存储，返回存储根目录。
func hlsFixture(t *testing.T, e *web.Echo) string {
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

// listHLS 列目录并返回 name → hls（缺字段按 false），同时把原始响应带回来供「根本没探测」的断言用。
func listHLS(t *testing.T, e *web.Echo, body string) (map[string]bool, string) {
	t.Helper()
	code, resp, raw := callRaw(t, e, http.MethodPost, "/api/fs/list", body, "")
	if code != 200 {
		t.Fatalf("列目录失败 body=%s code=%d msg=%v", body, code, resp["message"])
	}
	data, _ := resp["data"].(map[string]any)
	out := map[string]bool{}
	content, _ := data["content"].([]any)
	for _, it := range content {
		row, ok := it.(map[string]any)
		if !ok {
			continue
		}
		hls, _ := row["hls"].(bool)
		out[row["name"].(string)] = hls || row["hls"] == true
	}
	return out, raw
}

// TestFsListHLSProbe 列表的旁路清单探测：只有明确要（hls:true）才探，且「已切分」只认真清的清单。
//
// 后台靠这个字段把切过的视频标出来并收掉切分入口，所以三条语义必须钉住：
// 不传开关时一个 hls 键都不该出现（APP 不付探测成本）、建了空 .hls/xxx/ 不算切过
// （否则半途而废的目录会把重切的路堵死）、子目录与分页路径同样要探到。
func TestFsListHLSProbe(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cut.mp4"), []byte("a"))
	writeFile(t, filepath.Join(root, "fresh.mp4"), []byte("b"))
	// 只有 .hls/<name>/ 空目录、没有 index.m3u8：切到一半失败过，不当作已切分
	mkdir(t, filepath.Join(root, mediaindex.HLSDirName, "half.mp4"))
	writeFile(t, filepath.Join(root, "half.mp4"), []byte("c"))
	mkdir(t, filepath.Join(root, "movies"))
	writeFile(t, filepath.Join(root, "movies", "nested.mp4"), []byte("d"))
	addStorage(t, "/media", "Local", root)

	// 同层与子目录各切一部（名字都带扩展名，与 mediaindex 约定一致）
	writePlaylist(t, root, "cut.mp4")
	writePlaylist(t, filepath.Join(root, "movies"), "nested.mp4")

	// 1) 没要就不探：整个响应里不该出现 hls 键（false 会被 omitempty 吃掉，
	//    所以这里断的是「连探测出的 true 也没有」）。
	_, raw := listHLS(t, e, `{"path":"/media"}`)
	if strings.Contains(raw, `"hls"`) {
		t.Errorf("请求不带 hls 时不该下发该字段: %s", raw)
	}

	// 2) 要了就逐条标出来
	flags, _ := listHLS(t, e, `{"path":"/media","hls":true}`)
	if !flags["cut.mp4"] {
		t.Error("cut.mp4 有清单，应标为已切分")
	}
	if flags["fresh.mp4"] {
		t.Error("fresh.mp4 没清单，不该标")
	}
	if flags["half.mp4"] {
		t.Error("half.mp4 只有空的 .hls 目录，不该算已切分")
	}
	if flags["movies"] {
		t.Error("目录不是切过的视频，不该标")
	}

	// 3) 子目录里探的是自己那一层的 .hls
	flags, _ = listHLS(t, e, `{"path":"/media/movies","hls":true}`)
	if !flags["nested.mp4"] {
		t.Errorf("子目录里的清单也要探到，got %v", flags)
	}

	// 4) 分页实现（网格）是另一条代码路径，同样得标
	const fixHash2 = "b1b2c3d4e5f6a7b8c9d0a1b2c3d4e5f6a7b8c9d0"
	idx := `{"name":"cut.mp4","size_kb":1,"modified":"2026-01-01T00:00:00Z","sha1":"` + fixHash + `","is_new":0}` + "\n" +
		`{"name":"fresh.mp4","size_kb":1,"modified":"2026-01-01T00:00:00Z","sha1":"` + fixHash2 + `","is_new":0}` + "\n"
	writeTestFile(t, root, mediaindex.IndexFileName, idx)
	flags, _ = listHLS(t, e, `{"path":"/media","hls":true,"page":1,"per_page":10}`)
	if !flags["cut.mp4"] || flags["fresh.mp4"] {
		t.Errorf("分页列表的 hls 标记不对: %v", flags)
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
