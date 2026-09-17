package drivers_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/models"
)

func TestLocalDriverListStatOpen(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := &models.Storage{Driver: "Local", MountPath: "/m",
		Addition: `{"root_folder_path":"` + root + `"}`}
	d, err := drivers.Open(s)
	if err != nil {
		t.Fatalf("打开本地驱动失败: %v", err)
	}
	defer func() { _ = d.Close() }()

	entries, err := d.List("/")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应列出 2 个条目，got %d", len(entries))
	}

	st, err := d.Stat("/a.mp4")
	if err != nil || st.Size != 5 || st.IsDir {
		t.Fatalf("Stat = %+v, err=%v, want size=5 且非目录", st, err)
	}

	f, size, err := d.Open("/a.mp4")
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer func() { _ = f.(io.Closer).Close() }()
	if size != 5 {
		t.Errorf("size = %d, want 5", size)
	}
	got, err := io.ReadAll(f)
	if err != nil || string(got) != "video" {
		t.Errorf("内容 = %q, err=%v, want video", got, err)
	}
}

func TestListCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "in.mp4"), []byte("x"), 0o644)

	d, err := drivers.Open(&models.Storage{Driver: "Local",
		Addition: `{"root_folder_path":"` + root + `"}`})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	// ../ 应被清理掉，不能跳出 root
	entries, err := d.List("/../..")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	for _, e := range entries {
		if e.Name == "in.mp4" {
			return // 仍在 root 内，符合预期
		}
	}
	t.Errorf("路径逃逸或结果不符: %+v", entries)
}

func TestOpenRejectsUnknownDriver(t *testing.T) {
	if _, err := drivers.Open(&models.Storage{Driver: "webdav"}); err == nil {
		t.Error("未知驱动应报错")
	}
}

func TestSMBRequiresAddressAndShare(t *testing.T) {
	// 无网络环境，只验证配置校验：缺字段应在连接前就失败
	_, err := drivers.Open(&models.Storage{Driver: "smb", Addition: `{}`})
	if err == nil {
		t.Error("缺少 address/share_name 应报错")
	}
	_, err = drivers.Open(&models.Storage{Driver: "SMB",
		Addition: `{"address":"127.0.0.1","share_name":"x"}`})
	if err == nil {
		t.Error("连不上 Samba 时应报错（本机无服务）")
	}
}

func TestDriverNameIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Local", "local", ""} {
		s := &models.Storage{Driver: name, Addition: `{"root_folder_path":"` + root + `"}`}
		d, err := drivers.Open(s)
		if err != nil {
			t.Fatalf("driver=%q 应识别为本地: %v", name, err)
		}
		_ = d.Close()
	}
}
