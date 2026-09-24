package mediaindex_test

import (
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/mediaindex"
)

// countingDriver 计一个存储被读走了多少字节。嵌入真驱动，只覆盖 Open。
type countingDriver struct {
	drivers.Driver
	read *int64
}

func (c countingDriver) Open(rel string) (io.ReadSeeker, int64, error) {
	f, n, err := c.Driver.Open(rel)
	if err != nil {
		return nil, n, err
	}
	return &countingFile{ReadSeeker: f, read: c.read}, n, nil
}

type countingFile struct {
	io.ReadSeeker
	read *int64
}

func (f *countingFile) Read(p []byte) (int, error) {
	n, err := f.ReadSeeker.Read(p)
	atomic.AddInt64(f.read, int64(n))
	return n, err
}

// Close 转发给真流（真驱动返回的流都带 Close，包装层别把它弄丢）。
func (f *countingFile) Close() error {
	if c, ok := f.ReadSeeker.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

// countScan 造一个只有 name 的目录，跑一轮扫描，返回该文件被读走的字节数。
func countScan(t *testing.T, name string, body []byte) int64 {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	var read int64
	drv := countingDriver{Driver: openDriver(t, root), read: &read}
	defer func() { _ = drv.Close() }()
	if _, err := mediaindex.ScanDriver(drv); err != nil {
		t.Fatalf("扫描 %s 失败: %v", name, err)
	}
	return atomic.LoadInt64(&read)
}

// TestScanReadsEachFileOnce 首次索引一个媒体文件**只能整读一遍**（算 sha1 那一遍）。
//
// 守卫的是一类很容易复发的浪费：解析标签时把整个文件读进内存。
// 从前 buildRecord 走 `readAllBytes`（io.ReadAll）拿标签，于是一个 20GB 的视频
// 会被**完整读第二遍、且整份塞进内存**，只为拿几百字节的 album/artist
// （这两个字段目前没有任何代码或界面在读）。
// 实测（256MB×2 文件）：改前每轮新增分配 1144MB / 572MB，改后 0MB。
func TestScanReadsEachFileOnce(t *testing.T) {
	const size = 4 << 20
	body := make([]byte, size)

	for _, name := range []string{"movie.mkv", "song.mp3"} {
		got := countScan(t, name, body)
		pass := float64(got) / float64(size)
		t.Logf("%-10s 读走 %.2f 遍", name, pass)
		if pass > 1.05 {
			t.Errorf("%s 读了 %.2f 遍：算 sha1 只需一遍，多出来的是把文件整份读进内存解析标签", name, pass)
		}
	}
}

// TestRescanDoesNotReadFiles 增量重扫（大小/时间都没变）**不该再读文件内容**。
//
// 这守卫的是"索引慢"的另一半，比第一次那个更隐蔽：buildRecord 原先的触发条件写成
// `needExtract || rec.CaptureTime == ""`（音频还多几个 `|| rec.Album == ""`），
// 于是**天生没有该元数据**的文件（截图/导出图没有 EXIF、无标签音频）每次扫描都
// 被整读一遍重新解析 —— "增量扫描"于是每次都把整个库重读一遍，看起来就是"索引怎么这么慢"。
//
// 这里三类各放一个全零文件（没有 EXIF、没有标签），第二轮几乎不该读任何东西。
func TestRescanDoesNotReadFiles(t *testing.T) {
	const size = 4 << 20
	root := t.TempDir()
	for _, name := range []string{"movie.mkv", "song.mp3", "shot.jpg", "pic.png"} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var read int64
	drv := countingDriver{Driver: openDriver(t, root), read: &read}
	defer func() { _ = drv.Close() }()

	if _, err := mediaindex.ScanDriver(drv); err != nil {
		t.Fatalf("首次扫描失败: %v", err)
	}
	first := atomic.LoadInt64(&read)

	if _, err := mediaindex.ScanDriver(drv); err != nil {
		t.Fatalf("重扫失败: %v", err)
	}
	second := atomic.LoadInt64(&read) - first

	t.Logf("首次读走 %.1fMB，重扫读走 %d 字节（只该读回 .index.jsonl）", float64(first)/(1<<20), second)
	if second > 64<<10 {
		t.Errorf("重扫又读了 %d 字节（%.1f 个文件）：内容没变就不该重新解析元数据",
			second, float64(second)/float64(size))
	}
}
