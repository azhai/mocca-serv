//go:build !noweb

package handlers_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/handlers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// pngBytes 造一张真 PNG。类型判定是按内容嗅探的，随手拼几个字节过不了这一关，
// 所以这里老老实实编码一张图。
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 0x6C, G: 0x8B, B: 0x3D, A: 0xFF})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// postCover 发一次表单上传（call 助手只能发 JSON，封面是文件，用不了它）。
func postCover(t *testing.T, e *echo.Echo, token, name string, data []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if name != "" {
		if err := w.WriteField("name", name); err != nil {
			t.Fatalf("写 name 字段失败: %v", err)
		}
	}
	fw, err := w.CreateFormFile("file", "cover.png")
	if err != nil {
		t.Fatalf("创建表单文件失败: %v", err)
	}
	if _, err = fw.Write(data); err != nil {
		t.Fatalf("写表单文件失败: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("收尾表单失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/meta/cover", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码应为 200，got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err = json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法信封: %v, body=%s", err, rec.Body.String())
	}
	code, _ := resp["code"].(float64)
	return int(code), resp
}

// TestSaveCoverWritesHiddenFile 封面必须按约定落到数据目录的隐藏目录里，
// 且返回的相对路径能被 models 的还原函数拼回同一个文件。
func TestSaveCoverWritesHiddenFile(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "cov", "p", models.RoleAdmin)
	data := pngBytes(t)

	code, resp := postCover(t, e, tok, "/media/电影/a.mp4", data)
	if code != 200 {
		t.Fatalf("保存封面应成功，got %d, msg=%v", code, resp["message"])
	}
	payload, _ := resp["data"].(map[string]any)
	got, _ := payload["cover"].(string)
	// 调用方给的是媒体文件名，服务端去掉扩展名后按真实类型补 .png
	if want := models.CoverRelPath("a.png"); got != want {
		t.Fatalf("cover = %q, want %q", got, want)
	}
	// 相对路径（不是绝对路径）—— 数据目录搬家后记录仍然有效
	if filepath.IsAbs(got) {
		t.Errorf("cover 应为相对数据目录的路径，got %q", got)
	}

	abs := models.ResolveHiddenPath(config.Cfg.DataDir, got)
	onDisk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("封面未落盘到 %s: %v", abs, err)
	}
	// 前 512 字节被拿来嗅探类型，若忘了拼回去，落盘的图就会缺一段
	if !bytes.Equal(onDisk, data) {
		t.Errorf("落盘内容与上传不一致：%d 字节 vs %d 字节", len(onDisk), len(data))
	}
}

func TestSaveCoverSanitizesName(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "cov2", "p", models.RoleAdmin)
	data := pngBytes(t)
	dir := filepath.Join(config.Cfg.DataDir, models.HiddenDirName, models.CoversSubDirName)

	cases := []struct {
		name string
		want string
	}{
		{"../../etc/passwd", "passwd.png"},     // 路径穿越只留基名
		{`..\..\windows\evil.exe`, "evil.png"}, // Windows 分隔符同样收敛
		{"a.mp4", "a.png"},                     // 去掉原扩展名，换成真实类型
		{"你好世界.mkv", "你好世界.png"},               // 中文名保留（文件名分类不改动用户可读性）
		{".hidden", "hidden.png"},              // 不允许写出隐藏文件
		{`a/b:c*d?e.mp4`, "b-c-d-e.png"},       // 非法字符换成连字符
	}
	for _, tc := range cases {
		code, resp := postCover(t, e, tok, tc.name, data)
		if code != 200 {
			t.Fatalf("name=%q 应成功，got %d, msg=%v", tc.name, code, resp["message"])
		}
		got, _ := resp["data"].(map[string]any)["cover"].(string)
		if want := models.CoverRelPath(tc.want); got != want {
			t.Errorf("name=%q → cover %q, want %q", tc.name, got, want)
		}
		if _, err := os.Stat(filepath.Join(dir, tc.want)); err != nil {
			t.Errorf("name=%q 的文件不在封面目录里: %v", tc.name, err)
		}
	}
	// 穿越尝试不该在封面目录之外留下任何东西
	if _, err := os.Stat(filepath.Join(config.Cfg.DataDir, "..", "etc")); err == nil {
		t.Error("封面目录之外出现了文件")
	}
}

func TestSaveCoverRejectsNonImageAndOversize(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "cov3", "p", models.RoleAdmin)

	// 不是图片：扩展名/MIME 都改不了内容嗅探的结果
	if code, resp := postCover(t, e, tok, "a.mp4", []byte("this is not an image, just text")); code != 400 {
		t.Errorf("非图片应返回 400，got %d, resp=%v", code, resp)
	}
	// 超过 2MB
	big := make([]byte, handlers.MaxCoverBytes+1)
	copy(big, pngBytes(t))
	if code, _ := postCover(t, e, tok, "big.mp4", big); code != 400 {
		t.Errorf("超大封面应返回 400，got %d", code)
	}
	// 缺 name：拿不到文件名就不知道该覆盖哪一个，直接拒
	if code, _ := postCover(t, e, tok, "", pngBytes(t)); code != 400 {
		t.Errorf("缺少 name 应返回 400，got %d", code)
	}
}

func TestSaveCoverNeedsAdmin(t *testing.T) {
	e := newApp(t)

	// 未登录 401、普通用户 403 —— 走信封，用 call 即可（中间件在绑定表单之前就拦下了）
	if code, _ := call(t, e, http.MethodPost, "/api/meta/cover", "", ""); code != 401 {
		t.Errorf("未登录应返回 401，got %d", code)
	}
	userTok := makeUser(t, e, "plain", "p", models.RoleGeneral)
	if code, _ := call(t, e, http.MethodPost, "/api/meta/cover", "", userTok); code != 403 {
		t.Errorf("普通用户应返回 403，got %d", code)
	}
}

// TestCoverPathRoundTripWithMeta 封面路径存进元数据后能原样读回，
// 这条把「生成封面」与「保存元数据」两个接口的衔接钉住。
func TestCoverPathRoundTripWithMeta(t *testing.T) {
	e := newApp(t)
	tok := makeUser(t, e, "cov4", "p", models.RoleAdmin)

	_, resp := postCover(t, e, tok, "/media/a.mp4", pngBytes(t))
	cover, _ := resp["data"].(map[string]any)["cover"].(string)

	body := `{"path":"/media/a.mp4","kind":` + itoa(models.MediaVideo) + `,"size":1,"cover":"` + cover + `"}`
	if code, _ := call(t, e, http.MethodPost, "/api/meta/save", body, tok); code != 200 {
		t.Fatalf("保存带封面的元数据应成功，got %d", code)
	}
	_, resp = call(t, e, http.MethodGet, "/api/meta?path=/media/a.mp4", "", "")
	meta, _ := resp["data"].(map[string]any)["meta"].(map[string]any)
	if got, _ := meta["cover"].(string); got != cover {
		t.Errorf("cover 往返不一致: %q vs %q", got, cover)
	}
	// cover 必须是隐藏目录口径的路径，否则列目录时会把封面当素材混进去
	if !strings.HasPrefix(cover, models.HiddenDirName+"/") {
		t.Errorf("cover 应以 %s/ 开头，got %q", models.HiddenDirName, cover)
	}
}
