package handlers_test

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/azhai/mocca/models"
)

// TestMountPathNormalizedOnSave 挂载点在保存时就规范化：
// 开头补斜线、末尾去斜线，用户写 `media/` 存下来的就是 `/media`。
func TestMountPathNormalizedOnSave(t *testing.T) {
	e := newApp(t)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	for _, tc := range []struct{ input, want string }{
		{"media/", "/media"},
		{"/nas//movies/", "/nas/movies"},
	} {
		code, resp := call(t, e, http.MethodPost, "/api/storage/create",
			`{"mount_path":"`+tc.input+`","driver":"local","addition":"{}"}`, admin)
		if code != 200 {
			t.Fatalf("创建挂载点 %q 应成功，got %d msg=%v", tc.input, code, resp["message"])
		}
		if got, _ := resp["data"].(map[string]any)["mount_path"].(string); got != tc.want {
			t.Errorf("创建 %q → mount_path = %q, want %q", tc.input, got, tc.want)
		}
	}

	// 改挂载点同样规范化，且聚合树立刻反映新路径
	_, listed := call(t, e, http.MethodGet, "/api/storage/list", "", admin)
	target := 0
	for _, it := range listed["data"].([]any) {
		row, _ := it.(map[string]any)
		if row["mount_path"] == "/media" {
			target = int(row["id"].(float64))
		}
	}
	if target == 0 {
		t.Fatal("列表里没有 /media 挂载点")
	}
	if code, _ := call(t, e, http.MethodPost, "/api/storage/update",
		`{"id":`+itoa(target)+`,"mount_path":"/Media/"}`, admin); code != 200 {
		t.Fatalf("改挂载点应成功，got %d", code)
	}
	// /nas/movies 还在，所以根层是 [Media nas]（忽略大小写的字母序）
	if names, _, _ := listPage(t, e, "/", admin); !slices.Equal(names, []string{"Media", "nas"}) {
		t.Errorf("改完挂载点后根层应为 [Media nas]，got %v", names)
	}
}

// TestFsListAggregatesMountsFirst 列目录的展示顺序：
// 挂载点 → 目录 → 文件（视频 / 音频 / 图片 / 文本 / 其它），组内按名字的字母序。
// 同时验证聚合树只贡献挂载点：根层不会掺进任何真实目录。
func TestFsListAggregatesMountsFirst(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	other := t.TempDir()
	// /media 这一层：两个目录 + 各类文件 + 一个与挂载点同名的真实目录
	for _, f := range []string{"b.mp4", "a.mp4", "song.mp3", "pic.png", "note.txt", "raw.bin"} {
		writeFile(t, filepath.Join(root, f), []byte("x"))
	}
	mkdir(t, filepath.Join(root, "Zdir"), filepath.Join(root, "adir"), filepath.Join(root, "archive"))
	writeTestFile(t, other, "inside.mp4", "x")

	addStorage(t, "/media", "Local", root)
	addStorage(t, "/media/archive", "Local", other) // 深一层的挂载点
	addStorage(t, "/nas", "Local", other)           // 另一个顶层挂载点

	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// ① /media：挂载点在最前，其后目录，再按类型分组的文件
	names, total, nullContent := listPage(t, e, "/media", admin)
	if nullContent {
		t.Fatal("content 不该是 null，空列表也应是 []")
	}
	want := []string{
		"archive",      // 挂载点（与真实同名目录合并成一条）
		"adir", "Zdir", // 目录；字母序忽略大小写，所以 adir 在 Zdir 之前
		"a.mp4", "b.mp4", // 视频
		"song.mp3", // 音频
		"pic.png",  // 图片
		"note.txt", // 文本
		"raw.bin",  // 其它
	}
	if !slices.Equal(names, want) {
		t.Errorf("/media 顺序不符\n got = %v\nwant = %v", names, want)
	}
	if total != len(want) {
		t.Errorf("total = %d, want %d", total, len(want))
	}

	// ② 根层：只有挂载点，没有真实目录（`/` 不是任何存储的根）
	names, _, _ = listPage(t, e, "/", admin)
	if want := []string{"media", "nas"}; !slices.Equal(names, want) {
		t.Errorf("根层应只列挂载点 %v，got %v", want, names)
	}

	// ③ 中间层也会长出来：只挂了 /media/archive，所以 /media 必须有 archive 入口
	names, _, _ = listPage(t, e, "/media/archive", admin)
	if !slices.Equal(names, []string{"inside.mp4"}) {
		t.Errorf("/media/archive 应列出自身存储的内容，got %v", names)
	}
}

// TestFsListFallsBackForPathsOutsideMounts 没带挂载点前缀的历史路径仍然可用：
// 路径既不在挂载点内、这一层也没有虚拟挂载点时，退回「最长挂载点」兜底。
func TestFsListFallsBackForPathsOutsideMounts(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "nested"))
	writeFile(t, filepath.Join(root, "nested", "a.mp4"), []byte("x"))
	addStorage(t, "/media", "Local", root)

	admin := makeUser(t, e, "root", "p", models.RoleAdmin)
	names, _, _ := listPage(t, e, "/nested", admin)
	if !slices.Equal(names, []string{"a.mp4"}) {
		t.Errorf("/nested 应落到兜底存储里，got %v", names)
	}
}

// TestFsListMarksMountPoints 挂载点条目必须带 mount 标记。
// 挂载点也是目录（is_dir=true、type=0），不标记的话客户端只能给它画文件夹图标，
// 而它其实是「另一个存储的入口」——点进去会换一个存储根。
func TestFsListMarksMountPoints(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "sub"))
	addStorage(t, "/media", "Local", root)
	addStorage(t, "/nas", "Local", root)
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	// 根层：列出来的每一条都是挂载点
	_, resp := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/"}`, admin)
	content, _ := resp["data"].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("根层应有 2 个挂载点，got %v", content)
	}
	for _, it := range content {
		row := it.(map[string]any)
		if row["mount"] != true {
			t.Errorf("挂载点 %v 应带 mount 标记，got %v", row["name"], row)
		}
		if row["is_dir"] != true {
			t.Errorf("挂载点 %v 的 is_dir 应为 true", row["name"])
		}
	}

	// 存储内部：真实目录不带该字段（omitempty，老客户端拿不到多余的键）
	_, resp = call(t, e, http.MethodPost, "/api/fs/list", `{"path":"/media"}`, admin)
	content, _ = resp["data"].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("应只有 1 条真实条目，got %v", content)
	}
	row := content[0].(map[string]any)
	if _, ok := row["mount"]; ok {
		t.Errorf("真实目录不该带 mount 字段: %v", row)
	}
	if row["is_dir"] != true || row["name"] != "sub" {
		t.Errorf("真实目录字段异常: %v", row)
	}
}

// TestMountTreeRefreshAfterStorageChange 聚合树是内存缓存：
// 新增挂载点后无需重启就该出现在列表里，删除后立刻消失。
func TestMountTreeRefreshAfterStorageChange(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	addStorage(t, "/media", "Local", root)
	if names, _, _ := listPage(t, e, "/", admin); !slices.Equal(names, []string{"media"}) {
		t.Fatalf("根层应为 [media]，got %v", names)
	}

	addStorage(t, "/nas", "Local", root)
	if names, _, _ := listPage(t, e, "/", admin); !slices.Equal(names, []string{"media", "nas"}) {
		t.Errorf("新增挂载点后根层应为 [media nas]，got %v", names)
	}

	st, err := models.GetStorageByMountPath("/nas")
	if err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteStorage(st.ID); err != nil {
		t.Fatal(err)
	}
	if names, _, _ := listPage(t, e, "/", admin); !slices.Equal(names, []string{"media"}) {
		t.Errorf("删掉挂载点后根层应为 [media]，got %v", names)
	}
}
