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

// openDriver 打开一个指向 root 的本地驱动（用完自动关）。
func openDriver(t *testing.T, root string) drivers.Driver {
	t.Helper()
	s := &models.Storage{Driver: "Local", MountPath: "/m",
		Addition: `{"root_folder_path":"` + root + `"}`}
	drv, err := drivers.Open(s)
	if err != nil {
		t.Fatalf("打开本地驱动失败: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	return drv
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
	// 内容根嵌进 t.TempDir() 的子目录：默认 .mocca 在内容根内，随 TempDir 一起清理
	root := filepath.Join(t.TempDir(), "media")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
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

	// 外部工具写好海报+简介后，重扫应翻成 0。
	// 默认 meta_dir 是内容根下的 .mocca，所以写在 root/.mocca 下。
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
		p := filepath.Join(root, ".mocca", filepath.FromSlash(rel))
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
	if err := os.Remove(filepath.Join(root, ".mocca", filepath.FromSlash(summary))); err != nil {
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
	want := "ab/cd/" + "123456789012345678901234567890123456" + ".png"
	if poster != want {
		t.Errorf("poster 路径不符：\n got %s\nwant %s", poster, want)
	}
	summary, _ := mediaindex.SummaryRel(sh)
	if summary != strings.Replace(want, ".png", ".meta", 1) {
		t.Errorf("summary 路径不符：%s", summary)
	}
	if _, err := mediaindex.PosterRel("short"); err == nil {
		t.Error("非法 sha1 应报错")
	}
}

// TestScanIndexesCommonContainers 索引必须收录 .mkv/.mov/.flac/.gif 这类常见媒体。
//
// 这条守着一个极难查的错位：类型识别认识这些扩展名，而索引收录用的 MediaExts 曾经
// 只有 .mp4/.mp3/.png/.jpg/.jpeg。后果是这些文件能在列表里正常显示、却**永远进不了
// 索引**：拿不到 sha1，而 /meta/poster 只认索引里的 sha1 —— 海报一直 404，
// 刮削下来的封面也读不回来，现象极易被误判成"封面生成失败/第一次失败第二次才好"。
//
// 所以这里不测"某个函数返回 true"，而是走一次真实扫描，确认它们真的进了索引。
func TestScanIndexesCommonContainers(t *testing.T) {
	root := t.TempDir()
	names := []string{"a.mkv", "b.mov", "c.webm", "d.flac", "e.wav", "f.gif", "g.mp4"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x-"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if n := scan(t, root); n != len(names) {
		t.Fatalf("应索引 %d 个媒体文件（.mkv/.mov/.webm/.flac/.wav/.gif 都在内），got %d", len(names), n)
	}
	idx := readLines(t, filepath.Join(root, mediaindex.IndexFileName))
	for _, name := range names {
		rec, ok := idx[name]
		if !ok {
			t.Errorf("%s 没进索引：它拿不到 sha1，海报就会一直 404", name)
			continue
		}
		if rec.SHA1 != sha1hex("x-"+name) {
			t.Errorf("%s 的 sha1 不对: %s", name, rec.SHA1)
		}
	}
}

// TestRebuildFilesIncremental 重新索引 = **按增量重建**：判据是"这条记录与文件现在的
// 情况是否一致"，也就是逐条比 **name + size + modified**。三条性质分别钉住：
//
//  1. 一致 → 沿用旧行，**一个字节都不读**（返回的 rehashed 必须是 0）；
//  2. 不一致（文件真的被改动过）→ 重算 sha1，并把提取到的元数据刷新；
//  3. force 传了名字才强制重算 —— 留给"同名同大小、mtime 也被保留、内容却被换过"的极少数情况。
func TestRebuildFilesIncremental(t *testing.T) {
	const wrong = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	names := []string{"a.mkv", "b.mkv"}

	// setup 造两个文件并建好索引，然后把**索引里**的 sha1 全部改坏
	// （size/modified 保持真实），模拟"索引记录是旧的"。
	setup := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		for _, n := range names {
			if err := os.WriteFile(filepath.Join(root, n), []byte("x-"+n), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if n := scan(t, root); n != len(names) {
			t.Fatalf("应索引 %d 个文件，got %d", len(names), n)
		}
		idxPath := filepath.Join(root, mediaindex.IndexFileName)
		records := readLines(t, idxPath)
		lines := make([]string, 0, len(names))
		for _, n := range names {
			rec := records[n]
			rec.SHA1 = wrong
			b, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
		}
		if err := os.WriteFile(idxPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}

	t.Run("文件没变：沿用旧行，不读文件", func(t *testing.T) {
		root := setup(t)
		written, rehashed, err := mediaindex.RebuildFiles(openDriver(t, root), "/", nil)
		if err != nil {
			t.Fatalf("重建索引失败: %v", err)
		}
		if written != len(names) || rehashed != 0 {
			t.Errorf("应写 %d 条记录 / 重算 0 条，got %d / %d", len(names), written, rehashed)
		}
		after := readLines(t, filepath.Join(root, mediaindex.IndexFileName))
		for _, n := range names {
			if after[n].SHA1 != wrong {
				t.Errorf("%s 的 sha1 不该被动（size+modified 一致）: got %s", n, after[n].SHA1)
			}
		}
	})

	t.Run("文件真的变了：只重算那一条", func(t *testing.T) {
		root := setup(t)
		// 改写 a.mkv 的内容（mtime 随之变化），b.mkv 不动
		if err := os.WriteFile(filepath.Join(root, "a.mkv"), []byte("x-a.mkv-NEW"), 0o644); err != nil {
			t.Fatal(err)
		}
		written, rehashed, err := mediaindex.RebuildFiles(openDriver(t, root), "/", nil)
		if err != nil {
			t.Fatalf("重建索引失败: %v", err)
		}
		if written != 2 || rehashed != 1 {
			t.Errorf("应写 2 条记录 / 重算 1 条，got %d / %d", written, rehashed)
		}
		after := readLines(t, filepath.Join(root, mediaindex.IndexFileName))
		if after["a.mkv"].SHA1 != sha1hex("x-a.mkv-NEW") {
			t.Errorf("改过的 a.mkv 应重算: got %s", after["a.mkv"].SHA1)
		}
		if after["b.mkv"].SHA1 != wrong {
			t.Errorf("没动的 b.mkv 不该被重算: got %s", after["b.mkv"].SHA1)
		}
	})

	t.Run("force 点名才强制重算", func(t *testing.T) {
		root := setup(t)
		written, rehashed, err := mediaindex.RebuildFiles(openDriver(t, root), "/", []string{"a.mkv"})
		if err != nil {
			t.Fatalf("重建索引失败: %v", err)
		}
		if written != 2 || rehashed != 1 {
			t.Errorf("应写 2 条记录 / 重算 1 条，got %d / %d", written, rehashed)
		}
		after := readLines(t, filepath.Join(root, mediaindex.IndexFileName))
		if after["a.mkv"].SHA1 != sha1hex("x-a.mkv") {
			t.Errorf("force 里的 a.mkv 应被强制重算: got %s", after["a.mkv"].SHA1)
		}
		if after["b.mkv"].SHA1 != wrong {
			t.Errorf("没点名的 b.mkv 不该被重算: got %s", after["b.mkv"].SHA1)
		}
	})
}

// TestMediaKindMatchesIndexFilter 类型识别与索引收录必须同源。
// 各留一份扩展名清单是这类 bug 的温床：加了新格式只改一处，另一边就悄悄漏掉。
func TestMediaKindMatchesIndexFilter(t *testing.T) {
	for _, p := range []struct {
		name string
		want int
	}{
		{"a.mkv", models.MediaVideo}, {"b.mov", models.MediaVideo}, {"c.mp4", models.MediaVideo},
		{"d.flac", models.MediaAudio}, {"e.mp3", models.MediaAudio},
		{"f.gif", models.MediaImage}, {"g.jpeg", models.MediaImage},
		{"h.txt", models.MediaUnknown}, {"i.srt", models.MediaUnknown},
	} {
		if got := mediaindex.MediaKindOf(p.name); got != p.want {
			t.Errorf("MediaKindOf(%q) = %d, want %d", p.name, got, p.want)
		}
	}
	// MediaExts 是"收录什么"的对外口径，必须与 MediaKindOf 一致
	for _, ext := range mediaindex.MediaExts {
		if got := mediaindex.MediaKindOf("x" + ext); got == models.MediaUnknown {
			t.Errorf("MediaExts 里有 %s，但 MediaKindOf 认不出它", ext)
		}
	}
}
