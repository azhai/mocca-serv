package mediaindex_test

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
)

// scan 在临时目录上跑一次扫描，返回扫描到的媒体文件数。
func scan(t *testing.T, root string) int {
	t.Helper()
	s := &models.Storage{Driver: "Local", MountPath: "/m",
		Addition: `{"root_folder_path":"` + root + `"}`}
	var drv drivers.Driver
	drv, err := drivers.Open(s)
	if err != nil {
		t.Fatalf("打开本地驱动失败: %v", err)
	}
	n, err := mediaindex.ScanDriver(drv)
	_ = drv.Close()
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	return n
}

// readLines 读一份 .index.jsonl，按文件名归成 map。
func readLines(t *testing.T, p string) map[string]mediaindex.Record {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 %s: %v", p, err)
	}
	out := map[string]mediaindex.Record{}
	for _, line := range splitLines(string(b)) {
		if line == "" {
			continue
		}
		var r mediaindex.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("解析行 %q 失败: %v", line, err)
		}
		out[r.Name] = r
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func sha1hex(data string) string {
	h := sha1.Sum([]byte(data))
	return hex.EncodeToString(h[:])
}

func TestScanWritesPerDirIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "song.mp3"), []byte("music"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photos", "pic.JPG"), []byte("imgdata"), 0o644); err != nil {
		t.Fatal(err)
	}

	if n := scan(t, root); n != 3 {
		t.Fatalf("应索引 3 个媒体文件，got %d", n)
	}

	rootIdx := readLines(t, filepath.Join(root, mediaindex.IndexFileName))
	if _, err := os.Stat(filepath.Join(root, mediaindex.IndexFileName)); err != nil {
		t.Fatalf("根目录缺少 .index.jsonl: %v", err)
	}
	if len(rootIdx) != 2 {
		t.Fatalf("根目录索引应有 2 条，got %d", len(rootIdx))
	}
	mv := rootIdx["movie.mp4"]
	if mv.SizeKB != 1 {
		t.Errorf("movie.mp4 size_kb = %d, want 1（5B 向上取整）", mv.SizeKB)
	}
	if mv.SHA1 != sha1hex("video") {
		t.Errorf("movie.mp4 sha1 = %s, want %s", mv.SHA1, sha1hex("video"))
	}
	if mv.Modified == "" {
		t.Error("movie.mp4 缺 modified")
	}
	if !mediaindex.IsSHA1(mv.SHA1) {
		t.Errorf("sha1 不是 40 位十六进制: %q", mv.SHA1)
	}
	// .mocca 还没建 → 全部 is_new=1
	for name, r := range rootIdx {
		if r.IsNew != 1 {
			t.Errorf("%s is_new = %d, want 1", name, r.IsNew)
		}
	}
	// .jpg 全大写也要索引；子目录也有自己的索引
	subIdx := readLines(t, filepath.Join(root, "photos", mediaindex.IndexFileName))
	if r, ok := subIdx["pic.JPG"]; !ok || r.SHA1 != sha1hex("imgdata") {
		t.Errorf("photos 索引缺 pic.JPG 或其 sha1 错：%+v", subIdx)
	}
	// 点开头的条目不得被写进索引
	if _, ok := rootIdx[mediaindex.IndexFileName]; ok {
		t.Error(".index.jsonl 自身混进了索引")
	}
}

func TestIsNewFlipsWhenMoccaComplete(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := scan(t, root); n != 1 {
		t.Fatalf("应索引 1 个，got %d", n)
	}
	idx := readLines(t, filepath.Join(root, mediaindex.IndexFileName))
	if idx["movie.mp4"].IsNew != 1 {
		t.Fatal("初始应 is_new=1")
	}

	// 外部工具写好海报+简介后，重扫应翻成 0
	sh := sha1hex("video")
	poster, err := mediaindex.PosterRel(sh)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := mediaindex.SummaryRel(sh)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{poster, summary} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := scan(t, root); n != 1 {
		t.Fatalf("重扫应仍索引 1 个，got %d", n)
	}
	idx = readLines(t, filepath.Join(root, mediaindex.IndexFileName))
	if idx["movie.mp4"].IsNew != 0 {
		t.Fatalf("海报+简介齐备后 is_new 应为 0，got %d", idx["movie.mp4"].IsNew)
	}

	// 只缺简介 → 回到 1
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(summary))); err != nil {
		t.Fatal(err)
	}
	scan(t, root)
	idx = readLines(t, filepath.Join(root, mediaindex.IndexFileName))
	if idx["movie.mp4"].IsNew != 1 {
		t.Fatalf("缺简介时 is_new 应为 1，got %d", idx["movie.mp4"].IsNew)
	}
}

func TestScanRehashesChangedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan(t, root)
	if r := readLines(t, filepath.Join(root, mediaindex.IndexFileName))["movie.mp4"]; r.SHA1 != sha1hex("video") {
		t.Fatalf("初始 sha1 错：%q", r.SHA1)
	}
	// 改写内容（跨过 KB 边界，确定性触发重算），sha1 应变
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"),
		[]byte(strings.Repeat("x", 2048)), 0o644); err != nil {
		t.Fatal(err)
	}
	scan(t, root)
	wrap := readLines(t, filepath.Join(root, mediaindex.IndexFileName))["movie.mp4"]
	if wrap.SHA1 == sha1hex("video") {
		t.Fatalf("改写后 sha1 仍为旧值：%q", wrap.SHA1)
	}
}

func TestMetaRelSlicesSHA1(t *testing.T) {
	// 40 位合法 sha1：ab cd + 36 位
	sh := "abcd" + "123456789012345678901234567890123456"
	if !mediaindex.IsSHA1(sh) {
		t.Fatalf("测试的 sha1 不合法: %q (len=%d)", sh, len(sh))
	}
	poster, err := mediaindex.PosterRel(sh)
	if err != nil {
		t.Fatal(err)
	}

	// 对照用户示例：ab/cd/123...98 → 首 2 字符目录、次 2 字符二级目录、其余为文件名
	want := ".mocca/ab/cd/" + "123456789012345678901234567890123456" + ".png"
	if poster != want {
		t.Errorf("poster 路径不符：\n got %s\nwant %s", poster, want)
	}
	summary, _ := mediaindex.SummaryRel(sh)
	if summary != strings.Replace(want, ".png", ".json", 1) {
		t.Errorf("summary 路径不符：%s", summary)
	}
	if _, err := mediaindex.PosterRel("short"); err == nil {
		t.Error("非法 sha1 应报错")
	}
}
