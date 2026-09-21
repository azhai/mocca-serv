package handlers_test

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// fixHash 一个固定但合法的 sha1，测试里据此拼 .mocca 海报/简介路径。
const fixHash = "a1b2c3d4e5f6a7b8c9d0a1b2c3d4e5f6a7b8c9d0"

// seedIndexedMedia 在 root 下布好一个带索引与 .mocca 附加信息的视频。
// 返回 index 对应文件名（a.mp4）与海报字节。
func seedIndexedMedia(t *testing.T, root string) []byte {
	t.Helper()
	png := []byte("\x89PNG\r\n\x1a\nfake-poster-bytes")
	writeTestFile(t, root, "a.mp4", "video-content")
	writeTestFile(t, root, ".index.jsonl",
		`{"name":"a.mp4","size_kb":2,"modified":"2026-01-01T00:00:00Z","sha1":"`+fixHash+`","is_new":0}`+"\n")

	pr, err := mediaindex.PosterRel(fixHash)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := mediaindex.SummaryRel(fixHash)
	if err != nil {
		t.Fatal(err)
	}
	// .mocca 落在设备根（内容根 root 的上级），因为 seed 的 root 是设备根下的内容子目录
	metaRoot := filepath.Dir(root)
	writeTestFile(t, metaRoot, filepath.ToSlash(pr), string(png))
	writeTestFile(t, metaRoot, filepath.ToSlash(sr), `{"summary":"测试简介"}`)
	return png
}

// contentRoot 建一个「设备根 + 内容子目录」的本地内容根：本地驱动把 .mocca 落在
// 内容根的上级（设备根），让根嵌进 t.TempDir() 的子目录，父目录（=元数据根）也随之被清理。
func contentRoot(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "media")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestMetaPosterServesPngOr404(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	addStorage(t, "/media", "Local", root)
	png := seedIndexedMedia(t, root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", admin)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	// 海报存在：200 + image/png + 原样字节
	rec := get("/meta/poster?path=/media/a.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("海报应 200，got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if rec.Body.String() != string(png) {
		t.Errorf("海报字节不符，Got=%d bytes want=%d", rec.Body.Len(), len(png))
	}

	// 无该索引：404
	if rec := get("/meta/poster?path=/media/nope.mp4"); rec.Code != 404 {
		t.Errorf("无索引文件海报应 404，got %d", rec.Code)
	}
}

func TestFsInfoAggregates(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	seedIndexedMedia(t, root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/info", `{"path":"/media/a.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("fs/info 应 200，got %d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if d["sha1"] != fixHash {
		t.Errorf("sha1 = %v, want %s", d["sha1"], fixHash)
	}
	if d["poster"] != true {
		t.Errorf("poster 应存在，got %v", d["poster"])
	}
	if !contains(respBody(resp), "测试简介") {
		t.Errorf("简介应读回 .mocca json，body=%s", respBody(resp))
	}

	// 无索引条目的文件：sha1 空、poster false、meta 为 null
	if code2, resp2 := call(t, e, http.MethodPost, "/api/fs/info",
		`{"path":"/media/none"}`, admin); code2 == 200 {
		d2, _ := resp2["data"].(map[string]any)
		if d2["sha1"] != "" || d2["poster"] == true {
			t.Errorf("无索引应 sha1 空且 poster=false: %v", d2)
		}
		if d2["meta"] != nil {
			t.Errorf("无元数据该为 null 而非缺省结构: %v", d2["meta"])
		}
	}
}

func TestFsEditWritesSummaryJSON(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	seedIndexedMedia(t, root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	body := `{"path":"/media/a.mp4","name":"星际穿越","summary":"浩瀚宇宙",
		"director":"诺兰","cast":["马修","安妮"],"year":2014,"region":"美国","studio":"派拉蒙"}`
	if code, resp := call(t, e, http.MethodPost, "/api/fs/edit", body, admin); code != 200 {
		t.Fatalf("编辑应成功，got %d msg=%v", code, resp["message"])
	}
	// 改名真落盘：旧消失、新存在，扩展名保留
	if exists(filepath.Join(root, "a.mp4")) || !exists(filepath.Join(root, "星际穿越.mp4")) {
		t.Errorf("改名应真改磁盘: a.mp4=%v, 星际穿越.mp4=%v",
			exists(filepath.Join(root, "a.mp4")), exists(filepath.Join(root, "星际穿越.mp4")))
	}
	// 附加信息应以设备 .mocca/<sha1>.meta 为唯一来源落盘（不再进数据库）
	sr, err := mediaindex.SummaryRel(fixHash)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(root), filepath.ToSlash(sr)))
	if err != nil {
		t.Fatalf("应写出 .mocca 附加信息: %v", err)
	}
	for _, want := range []string{`"director": "诺兰"`, `"year": 2014`, "马修", "安妮"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".mocca JSON 缺 %q: %s", want, data)
		}
	}

	// 非管理员调编辑 → 信封 code 403
	user := makeUser(t, e, "plain", "p", models.RoleGeneral)
	if code, _ := call(t, e, http.MethodPost, "/api/fs/edit", body, user); code != 403 {
		t.Errorf("普通用户编辑应 403，got code=%d", code)
	}
}

func TestFsCovReplacesPoster(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	_ = seedIndexedMedia(t, root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 上传新封面：裸 body 放 PNG 字节
	newPng := makePNG(t)
	req := httptest.NewRequest(http.MethodPost, "/api/fs/cov?path=/media/a.mp4",
		bytesF(newPng))
	req.Header.Set("Authorization", admin)
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("上传封面应成功，got %d body=%s", rec.Code, rec.Body.String())
	}

	// 海报读回：封面统一缩放裁剪到 400×300，所以字节与上传不同，验证尺寸
	get := httptest.NewRequest(http.MethodGet, "/meta/poster?path=/media/a.mp4", nil)
	get.Header.Set("Authorization", admin)
	grec := httptest.NewRecorder()
	e.ServeHTTP(grec, get)
	img, _, derr := image.Decode(bytes.NewReader(grec.Body.Bytes()))
	if derr != nil {
		t.Fatalf("读回的海报不是可解码图片: %v", derr)
	}
	if b := img.Bounds(); b.Dx() != mediaindex.CoverWidth || b.Dy() != mediaindex.CoverHeight {
		t.Errorf("封面尺寸 = %dx%d, want %dx%d",
			b.Dx(), b.Dy(), mediaindex.CoverWidth, mediaindex.CoverHeight)
	}

	// 非图片内容应被拒
	bad := httptest.NewRequest(http.MethodPost, "/api/fs/cov?path=/media/a.mp4",
		bytesF([]byte("not-an-image")))
	bad.Header.Set("Authorization", admin)
	brec := httptest.NewRecorder()
	e.ServeHTTP(brec, bad)
	if brec.Code != http.StatusOK { // 信封式响应，HTTP 200 但 code 非 200
		t.Fatalf("非图片应 HTTP 200(信封) got %d", brec.Code)
	}
	if body := brec.Body.String(); !strings.Contains(body, "不是有效的图片") {
		t.Errorf("非图片应报错，body=%s", body)
	}
}

func TestFsRemoveCleansDevice(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	seedIndexedMedia(t, root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 先编辑一次，确保确有文件待删
	edit := `{"path":"/media/a.mp4","name":"a","summary":"删除测试","year":2020}`
	if code, resp := call(t, e, http.MethodPost, "/api/fs/edit", edit, admin); code != 200 {
		t.Fatalf("预写附加信息失败 code=%d msg=%v", code, resp["message"])
	}
	// 删除文件
	if code, resp := call(t, e, http.MethodPost, "/api/fs/remove", `{"path":"/media/a.mp4"}`, admin); code != 200 {
		t.Fatalf("删除应成功 code=%d msg=%v", code, resp["message"])
	}
	if exists(filepath.Join(root, "a.mp4")) {
		t.Errorf("文件应被删除")
	}
}

// bytesF 用一个短 reader 包字节，供裸 body 上传测试使用。
func bytesF(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// makePNG 编码一张最小合法 PNG，供 FsCov 的图片校验通过。
func makePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码测试 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// makeTinyVideo 用 ffmpeg 生成一段 2 秒的迷你视频；本机没有 ffmpeg 则跳过截图测试。
// 秒数给足 2 秒，保证 seek 到第 1 秒时还能抽到帧。
// FsShot 是纯 ffmpeg 能力，缺它就无从验起，跳过而不报红。
func makeTinyVideo(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机未安装 ffmpeg，跳过 FsShot 截图集成测试")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "v.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=1",
		"-t", "2", "-pix_fmt", "yuv420p", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg 生成测试视频失败: %v\n%s", err, out)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestFsShotRejectsNonVideo 非视频文件不得触发 FFmpeg 截图。
func TestFsShotRejectsNonVideo(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	addStorage(t, "/media", "Local", root)
	writeTestFile(t, root, "a.txt", "hello")
	writeTestFile(t, root, ".index.jsonl",
		`{"name":"a.txt","size_kb":1,"modified":"2026-01-01T00:00:00Z","sha1":"`+fixHash+`","is_new":0}`+"\n")
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/shot?path=/media/a.txt&sec=0", "", admin)
	if code == 200 {
		t.Fatalf("非视频不应截图，got code=%d", code)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "仅视频") {
		t.Fatalf("应提示仅视频支持，got %q", msg)
	}
}

// TestFsGetServesIndexedMedia 网格点封面播放走 /fs/get：已索引的媒体必须 200 并给 raw_url。
// 若此测试失败即复现「点击封面显示文件不存在」。
func TestFsGetServesIndexedMedia(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	seedIndexedMedia(t, root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/a.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("已索引媒体 /fs/get 应 200，got %d msg=%v", code, resp["message"])
	}
	d, _ := resp["data"].(map[string]any)
	if raw, _ := d["raw_url"].(string); raw == "" {
		t.Errorf("raw_url 应为取流地址，got %q", raw)
	}
}

// TestFsGetTrailingSlashMount 挂载点若存了尾斜杠（历史不规范写法），
// FsList 与 FsGet 都按 NormalizeMountPath 后的挂载点匹配，路径口径一致：
// 列表能看、播放不 404（resolveStorage 与 resolveStorageStrict 统一口径的回归）。
func TestFsGetTrailingSlashMount(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media/", "Local", root)
	seedIndexedMedia(t, root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	if code, resp := call(t, e, http.MethodPost, "/api/fs/list",
		`{"path":"/media"}`, admin); code != 200 {
		t.Fatalf("列表应 200，got %d msg=%v", code, resp["message"])
	}
	code, resp := call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/a.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("尾斜杠挂载点下 /fs/get 应 200，got %d msg=%v", code, resp["message"])
	}
}

// TestFsGetRootMountPrefersRoot 根挂载与子挂载并存时：根挂载下的文件必须路由到
// 根存储，而不是被 fallback 错误路由到子挂载存储（否则 /fs/get 404「文件不存在」）。
func TestFsGetRootMountPrefersRoot(t *testing.T) {
	e := newApp(t)
	rootA := contentRoot(t) // 根挂载 / 的内容
	rootB := contentRoot(t) // 子挂载 /media 的内容
	writeTestFile(t, rootA, "a.mp4", "video-content")
	writeTestFile(t, rootB, "b.mp4", "video-content")
	addStorage(t, "/", "Local", rootA)
	addStorage(t, "/media", "Local", rootB)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 根挂载下的文件：路由到 rootA
	code, resp := call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/a.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("根挂载文件 /fs/get 应 200，got %d msg=%v", code, resp["message"])
	}
	if raw := rawURLOf(t, e, "/a.mp4", admin); !strings.Contains(raw, "/d/a.mp4") {
		t.Errorf("raw_url 应为 /d/a.mp4，got %q", raw)
	}

	// 子挂载下的文件：路由到 rootB（不应被根挂载抢走）
	code, resp = call(t, e, http.MethodPost, "/api/fs/get", `{"path":"/media/b.mp4"}`, admin)
	if code != 200 {
		t.Fatalf("子挂载文件 /fs/get 应 200，got %d msg=%v", code, resp["message"])
	}
	if raw := rawURLOf(t, e, "/media/b.mp4", admin); !strings.Contains(raw, "/d/media/b.mp4") {
		t.Errorf("raw_url 应为 /d/media/b.mp4，got %q", raw)
	}
}

// TestFsShotReadsJSONBody 前端以 JSON body 提交 {path,sec}（不是 query 串），
// 后端必须能解析出 path；解析失败会报「缺少 path」而非「仅视频」。
func TestFsShotReadsJSONBody(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	addStorage(t, "/media", "Local", root)
	writeTestFile(t, root, "a.txt", "hello")
	writeTestFile(t, root, ".index.jsonl",
		`{"name":"a.txt","size_kb":1,"modified":"2026-01-01T00:00:00Z","sha1":"`+fixHash+`","is_new":0}`+"\n")
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/shot",
		`{"path":"/media/a.txt","sec":0}`, admin)
	if code == 200 {
		t.Fatalf("非视频不应截图，got code=%d", code)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "仅视频") {
		t.Fatalf("JSON body 应解析出 path（提示仅视频而非缺少 path），got %q", msg)
	}
}

// TestFsShotWritesFrameAsPoster 真实视频经 FFmpeg 截图后，封面位置出现可解码 PNG。
func TestFsShotWritesFrameAsPoster(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)

	video := makeTinyVideo(t)
	writeTestFile(t, root, "v.mp4", string(video))
	writeTestFile(t, root, ".index.jsonl",
		`{"name":"v.mp4","size_kb":2,"modified":"2026-01-01T00:00:00Z","sha1":"`+fixHash+`","is_new":0}`+"\n")
	pr, err := mediaindex.PosterRel(fixHash)
	if err != nil {
		t.Fatal(err)
	}
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/shot?path=/media/v.mp4&sec=1", "", admin)
	if code != 200 {
		t.Fatalf("截图应成功, code=%d msg=%v", code, resp["message"])
	}
	// 海报写在设备根（内容根上级）的 .mocca 下，应出现可解码的 PNG
	data, err := os.ReadFile(filepath.Join(filepath.Dir(root), filepath.ToSlash(pr)))
	if err != nil {
		t.Fatalf("海报未写入: %v", err)
	}
	if _, _, derr := image.Decode(bytes.NewReader(data)); derr != nil {
		t.Fatalf("写出的海报不是合法图片: %v", derr)
	}
}

// mediaedit_test.go 仅用 helper；占位引用避免未用告警（与 fsops_test 同风格）。
var _ = echo.Context{}
