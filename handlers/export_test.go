package handlers_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// exportPost 以**原始字节**方式请求导出接口。
// 不能用 call()：那把响应体当 JSON 解析，而这里正常时是二进制 tar.gz。
func exportPost(t *testing.T, e *echo.Echo, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/fs/export", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// readTarGz 解开响应体，返回「tar 内路径 → 内容」。
func readTarGz(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("响应不是 gzip: %v", err)
	}
	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("读 tar 失败: %v", err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", h.Name, err)
		}
		out[h.Name] = string(b)
	}
	return out
}

// TestFsExportPacksMetaData 数据迁移：勾选视频在 .mocca 里的三份数据（海报 / 附加信息 /
// 弹幕评论）保持 ab/cd 子目录一并打包，路径带 .mocca/ 前缀（解压到设备根即还原），
// 包根另附 manifest.json 说明视频与 sha1、包内文件清单的对应关系。
func TestFsExportPacksMetaData(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	png := seedIndexedMedia(t, root) // a.mp4 + .index.jsonl + 海报 + 附加信息
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	pr, err := mediaindex.PosterRel(fixHash)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := mediaindex.SummaryRel(fixHash)
	if err != nil {
		t.Fatal(err)
	}
	dr, err := mediaindex.DanmakuRel(fixHash)
	if err != nil {
		t.Fatal(err)
	}
	danmaku := `{"id":"1","type":1,"offset":100,"content":"来了"}` + "\n"
	writeTestFile(t, root, filepath.Join(".mocca", filepath.ToSlash(dr)), danmaku)

	rec := exportPost(t, e, `{"paths":["/media/a.mp4"]}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 应 200，got %d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q，应为 application/gzip", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q，应含 no-store", cc)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `attachment`) ||
		!strings.Contains(cd, ".tar.gz") {
		t.Errorf("Content-Disposition = %q，应带 attachment 与 .tar.gz 文件名", cd)
	}

	files := readTarGz(t, rec)
	want := map[string]string{
		".mocca/" + filepath.ToSlash(pr): string(png),
		".mocca/" + filepath.ToSlash(sr): `{"summary":"测试简介"}`,
		".mocca/" + filepath.ToSlash(dr): danmaku,
	}
	for name, content := range want {
		got, ok := files[name]
		if !ok {
			t.Errorf("包内缺少 %s（实际条目 %v）", name, files)
			continue
		}
		if got != content {
			t.Errorf("%s 内容不符，got %q want %q", name, got, content)
		}
	}
	if len(files) != 4 { // 三份数据 + manifest.json
		t.Errorf("包内应 4 个条目，got %d：%v", len(files), files)
	}

	var man struct {
		Version int    `json:"version"`
		MetaDir string `json:"meta_dir"`
		Items   []struct {
			Path  string   `json:"path"`
			SHA1  string   `json:"sha1"`
			Files []string `json:"files"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(files["manifest.json"]), &man); err != nil {
		t.Fatalf("manifest.json 解析失败: %v", err)
	}
	if man.MetaDir != ".mocca" {
		t.Errorf("manifest.meta_dir = %q，应为 .mocca", man.MetaDir)
	}
	if len(man.Items) != 1 || man.Items[0].Path != "/media/a.mp4" ||
		man.Items[0].SHA1 != fixHash || len(man.Items[0].Files) != 3 {
		t.Errorf("manifest.items 不符: %+v", man.Items)
	}
}

// TestFsExportComputesSHA1WhenUnindexed 目录没有索引（或索引里没有这条记录）时也要能导出：
// 按文件内容现算 sha1 再去找 .mocca 数据。这正是"刮削过、却因为索引没跟上而导不出来"的场景。
func TestFsExportComputesSHA1WhenUnindexed(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	const content = "video-content-without-index"
	writeTestFile(t, root, "c.mp4", content)
	sum := sha1.Sum([]byte(content))
	wantSHA := hex.EncodeToString(sum[:])

	pr, err := mediaindex.PosterRel(wantSHA)
	if err != nil {
		t.Fatal(err)
	}
	png := "\x89PNG\r\n\x1a\nposter"
	writeTestFile(t, root, filepath.Join(".mocca", filepath.ToSlash(pr)), png)
	// 刻意不写 .index.jsonl

	rec := exportPost(t, e, `{"paths":["/media/c.mp4"]}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 应 200，got %d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("没索引时也应回 tar.gz，Content-Type = %q body=%q", ct, rec.Body.String())
	}
	files := readTarGz(t, rec)
	if got := files[".mocca/"+filepath.ToSlash(pr)]; got != png {
		t.Errorf("包内缺海报或内容不符，got %q（实际条目 %v）", got, files)
	}
	var man struct {
		Items []struct {
			SHA1  string   `json:"sha1"`
			Note  string   `json:"note"`
			Files []string `json:"files"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(files["manifest.json"]), &man); err != nil {
		t.Fatalf("manifest.json 解析失败: %v", err)
	}
	if len(man.Items) != 1 || man.Items[0].SHA1 != wantSHA || len(man.Items[0].Files) != 1 {
		t.Errorf("manifest.items 不符: %+v", man.Items)
	}
	if man.Items[0].Note == "" {
		t.Errorf("按内容现算指纹时应在 note 里说明，got 空")
	}
}

// TestFsExportRejectsNoData 没有可导出数据时回 JSON 信封的 404，而不是发一个空包 ——
// 空归档对用户没有任何用处。
func TestFsExportRejectsNoData(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	// 没写 .index.jsonl：会按文件内容现算 sha1，但 .mocca 里也没有对应的数据 → 仍无可导出
	writeTestFile(t, root, "b.mp4", "video-content")
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	code, resp := call(t, e, http.MethodPost, "/api/fs/export", `{"paths":["/media/b.mp4"]}`, admin)
	if code != 404 {
		t.Errorf("没有可导出数据时 business code = %d，应为 404", code)
	}
	if resp["message"] == "" || !strings.Contains(resp["message"].(string), "没有可导出") {
		t.Errorf("message = %v，应说明没有可导出的数据", resp["message"])
	}
	// 路径不属于任何挂载点：同样是 404，但走的是另一条分支
	code, _ = call(t, e, http.MethodPost, "/api/fs/export", `{"paths":["/nope/a.mp4"]}`, admin)
	if code != 404 {
		t.Errorf("路径不属于任何挂载点时 code = %d，应为 404", code)
	}
}
