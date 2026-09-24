package mediaindex_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azhai/mocca/mediaindex"
)

// 这一组钉住「从文件里提取到的元数据」这件事本身。
//
// 原来提取路径**一条测试都没有**，而改动它很容易悄悄改坏：解析标签/EXIF 的入参
// 从「整份字节」换成了「驱动给的流」（见 scan.go 的 withMedia），若哪天有人又写成
// 只读文件头、或加个 LimitReader 限得太狠，解析就会静默失败 —— 索引照写、字段空着，
// 看不出任何异常。所以这里用现造的夹具把「解析得出正确值」钉死。

// id3v2Frame 造一个 ID3v2.3 文本帧：帧头 10 字节（ID + 大端长度 + 标志）+ 编码字节 + 文本。
func id3v2Frame(id, text string) []byte {
	body := append([]byte{0x03}, []byte(text)...) // 0x03 = UTF-8
	b := append([]byte{}, id...)
	n := len(body)
	b = append(b, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	b = append(b, 0x00, 0x00) // 帧标志
	return append(b, body...)
}

// id3v2Tag 造一个完整 ID3v2.3 标签：10 字节标签头（同步安全长度）+ 若干帧。
func id3v2Tag(frames ...[]byte) []byte {
	var body []byte
	for _, f := range frames {
		body = append(body, f...)
	}
	out := []byte{'I', 'D', '3', 0x03, 0x00, 0x00}
	n := len(body)
	out = append(out, byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f))
	return append(out, body...)
}

// jpegWithDateTimeOriginal 造一张最小 JPEG：SOI + APP1(Exif + TIFF/IFD0 里一个
// DateTimeOriginal) + EOI。真实照片的 EXIF 也是这样紧跟在文件最前面。
func jpegWithDateTimeOriginal(dt string) []byte {
	ascii := append([]byte(dt), 0x00) // ASCII 值需以 NUL 结尾，19+1=20 字节

	tiff := []byte{'I', 'I', 0x2A, 0x00}        // 小端字节序 + 魔数 42
	tiff = append(tiff, 0x08, 0x00, 0x00, 0x00) // IFD0 起始偏移 = 8
	ifd := []byte{0x01, 0x00}                   // 1 个条目
	ifd = append(ifd,
		0x03, 0x90, // tag 0x9003 = DateTimeOriginal
		0x02, 0x00, // type 2 = ASCII
		0x14, 0x00, 0x00, 0x00, // count 20
		0x1A, 0x00, 0x00, 0x00, // 值偏移 26（紧跟 IFD0 之后）
	)
	ifd = append(ifd, 0x00, 0x00, 0x00, 0x00) // 没有下一个 IFD
	tiff = append(tiff, ifd...)
	tiff = append(tiff, ascii...)

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segLen := len(payload) + 2 // 段长含自身的 2 字节
	seg := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	seg = append(seg, payload...)

	out := []byte{0xFF, 0xD8} // SOI
	out = append(out, seg...)
	return append(out, 0xFF, 0xD9) // EOI
}

// TestExtractImageCaptureTime 图片：EXIF 的 DateTimeOriginal 必须落到 capture_time。
func TestExtractImageCaptureTime(t *testing.T) {
	const dt = "2019:08:12 20:31:45"
	// 解析按本地时区，输出统一转 UTC（与 ImageCaptureTime 的实现一致）
	want, err := time.ParseInLocation("2006:01:02 15:04:05", dt, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	wantUTC := want.UTC().Format(time.RFC3339)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shot.jpg"), jpegWithDateTimeOriginal(dt), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := scan(t, root); n != 1 {
		t.Fatalf("应索引 1 个文件，got %d", n)
	}

	rec := readLines(t, filepath.Join(root, mediaindex.IndexFileName))["shot.jpg"]
	if rec.CaptureTime != wantUTC {
		t.Errorf("capture_time = %q, want %q（EXIF 没解析出来）", rec.CaptureTime, wantUTC)
	}
}

// TestExtractAudioTags 音频：内嵌的专辑/作者必须落到 album/artist。
func TestExtractAudioTags(t *testing.T) {
	mp3 := id3v2Tag(id3v2Frame("TALB", "夜曲"), id3v2Frame("TPE1", "周杰伦"))
	// 标签之后接一段假音频数据，确认解析不依赖文件长度
	mp3 = append(mp3, make([]byte, 2048)...)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "song.mp3"), mp3, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := scan(t, root); n != 1 {
		t.Fatalf("应索引 1 个文件，got %d", n)
	}

	rec := readLines(t, filepath.Join(root, mediaindex.IndexFileName))["song.mp3"]
	if rec.Album != "夜曲" || rec.Artist != "周杰伦" {
		t.Errorf("album/artist = %q/%q, want 夜曲/周杰伦", rec.Album, rec.Artist)
	}
	if len(rec.SHA1) != 40 {
		t.Errorf("sha1 = %q", rec.SHA1)
	}
}

// TestExtractAudioTagsAfterReindex 增量扫描也要能拿到标签：
// 第二轮（size/modified 未变）不该再整读文件，但**第一轮的结果必须留着**。
func TestExtractAudioTagsAfterReindex(t *testing.T) {
	mp3 := append(id3v2Tag(id3v2Frame("TPE1", "巴赫")), make([]byte, 512)...)
	root := t.TempDir()
	p := filepath.Join(root, "b.mp3")
	if err := os.WriteFile(p, mp3, 0o644); err != nil {
		t.Fatal(err)
	}

	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	for i := 1; i <= 2; i++ {
		if _, err := mediaindex.ScanDriver(drv); err != nil {
			t.Fatal(err)
		}
		rec := readLines(t, filepath.Join(root, mediaindex.IndexFileName))["b.mp3"]
		if rec.Artist != "巴赫" {
			t.Errorf("第 %d 轮 artist = %q, want 巴赫", i, rec.Artist)
		}
	}
}
