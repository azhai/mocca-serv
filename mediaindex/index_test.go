package mediaindex_test

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/azhai/mocca/mediaindex"
)

// TestReadIndexPagePaging 验证分页按 .index.jsonl 行区间切、total 正确。
func TestReadIndexPagePaging(t *testing.T) {
	var sb strings.Builder
	names := []string{"a.mp4", "b.mp4", "c.mp4", "d.mp4", "e.mp4", "f.mp4"}
	for _, n := range names {
		sb.WriteString(`{"name":"` + n + `","size_kb":1,"modified":"2026-01-01T00:00:00Z","sha1":"abc` +
			strings.Repeat("0", 37) + `","is_new":0}` + "\n")
	}
	content := sb.String()

	// 第 2 页，每页 3：应取 c/d/e 之外的中间三行，total=6
	page, total, err := mediaindex.ReadIndexPage(strings.NewReader(content), 1, 3)
	if err != nil {
		t.Fatalf("ReadIndexPage 报错: %v", err)
	}
	if total != 6 {
		t.Fatalf("total = %d, want 6", total)
	}
	wantNames := []string{"b.mp4", "c.mp4", "d.mp4"}
	if len(page) != 3 {
		t.Fatalf("本页行数 = %d, want 3", len(page))
	}
	for i, w := range wantNames {
		if page[i].Name != w {
			t.Errorf("第 %d 行名 = %q, want %q", i, page[i].Name, w)
		}
	}

	// 越界 offset 夹到末尾
	page, total, _ = mediaindex.ReadIndexPage(strings.NewReader(content), 10, 3)
	if len(page) != 0 || total != 6 {
		t.Errorf("越界应返回空页且 total=6, got len=%d total=%d", len(page), total)
	}

	// limit<=0 读完整区间
	page, total, _ = mediaindex.ReadIndexPage(strings.NewReader(content), 0, 0)
	if len(page) != 6 || total != 6 {
		t.Errorf("limit<=0 应读全部, got len=%d total=%d", len(page), total)
	}
}

// TestRecordJSONIncludesNewKeys 验证 .index.jsonl 每行包含新字段且键有序。
func TestRecordJSONIncludesNewKeys(t *testing.T) {
	line, err := mediaindex.MarshalRecord(mediaindex.Record{
		Name: "a.mp4", SizeKB: 1, Modified: "2026-01-01T00:00:00Z",
		SHA1: "abc" + strings.Repeat("0", 37), IsNew: 0,
		CaptureTime: "2026-09-20T00:00:00Z", Album: "夜曲", Artist: "周杰伦",
	})
	if err != nil {
		t.Fatalf("MarshalRecord 报错: %v", err)
	}
	hasKey := func(k string) bool { return bytes.Contains(line, []byte(`"`+k+`":`)) }
	for _, k := range []string{"album", "artist", "capture_time", "is_new", "modified", "name", "sha1", "size_kb"} {
		if !hasKey(k) {
			t.Errorf("索引行缺键 %q: %s", k, line)
		}
	}
	// 键按字母升序：album < artist < capture_time < is_new < ...
	a, b := bytes.Index(line, []byte(`"album":`)), bytes.Index(line, []byte(`"artist":`))
	if a < 0 || b < 0 || a > b {
		t.Errorf("album 应在 artist 之前（map 升序）: %s", line)
	}
}

// TestResizeCoverPNGCropsTo400x300 验证封面统一缩放裁剪到 400×300、PNG 可解码。
func TestResizeCoverPNGCropsTo400x300(t *testing.T) {
	// 竖构图 100×300：等比放大 → 400×1200 → 居中去顶底 → 400×300
	img := image.NewRGBA(image.Rect(0, 0, 100, 300))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out, err := mediaindex.ResizeCoverPNG(buf.Bytes())
	if err != nil {
		t.Fatalf("ResizeCoverPNG 报错: %v", err)
	}
	dec, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("输出不是可解码图片: %v", err)
	}
	b := dec.Bounds()
	if b.Dx() != mediaindex.CoverWidth || b.Dy() != mediaindex.CoverHeight {
		t.Errorf("封面尺寸 = %dx%d, want %dx%d",
			b.Dx(), b.Dy(), mediaindex.CoverWidth, mediaindex.CoverHeight)
	}
}
