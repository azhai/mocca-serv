package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/labstack/echo/v5"
)

// dotEntries 点开头的隐藏项（文件与目录都算），一律不下发。
var dotEntries = []string{
	".DS_Store", ".hidden.mp4", "._movie.mp4", ".git", ".mocca",
}

// reservedEntries 系统保留名：按**完整名字**匹配（不区分大小写），一律不下发。
// 用真实世界的写法，逐个覆盖 handlers 里 systemReservedNames 的每一项 ——
// 实现里少写一项，这里就会失败。
var reservedEntries = []string{
	"$RECYCLE.BIN", "System Volume Information", "RECYCLER",
	"Thumbs.db", "ehthumbs.db", "desktop.ini",
	"lost+found", "found.000",
	"hiberfil.sys", "pagefile.sys", "swapfile.sys",
	"Network Trash Folder", "Temporary Items",
}

// reservedDirs 其中在真实系统里是目录的，建成目录以覆盖「目录也参与过滤」。
var reservedDirs = []string{"$RECYCLE.BIN", "System Volume Information", "lost+found"}

// visibleDirs 可见目录。
var visibleDirs = []string{"sub", "sub2"}

// visibleEntries 必须原样保留的条目。
//
// 重点是那批「以特殊符号开头、但不是系统保留名」的正常文件 —— 早先按首字符
// 挡 `!@#$%^&*?` 会把它们一起误伤，本次收窄规则正是为了它们。
// 另有中文名（首字节 ≥ 0x80，防按字节误判）与「只差一点的保留名」
// （`$RECYCLE.BIN.mp4`、`lost+found2`）—— 精确匹配才不会被它们骗过。
var visibleEntries = []string{
	"movie.mp4", "zz.mp4",
	"爱在黎明破晓前.mp4", "神偷奶爸！.mp4", "C++入门.mp4",
	"#1 Hits.mp4", "!important.mp3", "@2x.png", "*待定.mp4", "?what.mp4",
	"&amp;tag.mp4", "^caret.mp4", "%off.mp4", "a#b.mp4",
	"$RECYCLE.BIN.mp4", "lost+found2", "Thumbs.db.bak", "System Volume Information.txt",
}

// excludedEntries 全部应被排除的条目。
func excludedEntries() []string {
	return append(slices.Clone(dotEntries), reservedEntries...)
}

// seedMixedDir 造一个可见项与排除项混排的目录。
func seedMixedDir(t *testing.T, root string) {
	t.Helper()
	for _, n := range visibleEntries {
		writeFile(t, filepath.Join(root, n), []byte("x"))
	}
	mkdir(t, filepath.Join(root, visibleDirs[0]), filepath.Join(root, visibleDirs[1]))

	// 点开头：.git 与自建的 .mocca（后者里面再放一层，确认整棵子树都不露头）
	mkdir(t, filepath.Join(root, ".git"), filepath.Join(root, ".mocca", "covers"))
	for _, n := range []string{".DS_Store", ".hidden.mp4", "._movie.mp4"} {
		writeFile(t, filepath.Join(root, n), []byte("x"))
	}
	writeFile(t, filepath.Join(root, ".mocca", "covers", "c.jpg"), []byte("x"))

	// 系统保留名：该是目录的建成目录，其余建成文件
	for _, n := range reservedEntries {
		if slices.Contains(reservedDirs, n) {
			mkdir(t, filepath.Join(root, n))
			continue
		}
		writeFile(t, filepath.Join(root, n), []byte("x"))
	}

	// 可见目录里的排除项，不该影响父目录这一层
	writeFile(t, filepath.Join(root, visibleDirs[0], ".secret"), []byte("x"))
	writeFile(t, filepath.Join(root, visibleDirs[0], "inside.mp4"), []byte("x"))
}

// listPage 调 fs/list，返回按**返回顺序**排列的名字、total，
// 以及 content 是否为 null（APP 拿到 null 会当成缺字段，不能出现）。
func listPage(t *testing.T, e *echo.Echo, path, token string) (names []string, total int, nullContent bool) {
	t.Helper()
	code, resp := call(t, e, http.MethodPost, "/api/fs/list", `{"path":"`+path+`"}`, token)
	if code != 200 {
		t.Fatalf("列目录失败 code=%d msg=%v", code, resp["message"])
	}
	data, _ := resp["data"].(map[string]any)
	raw, ok := data["content"]
	if !ok || raw == nil {
		return nil, 0, true
	}
	content, _ := raw.([]any)
	for _, it := range content {
		m, _ := it.(map[string]any)
		names = append(names, m["name"].(string))
	}
	return names, int(data["total"].(float64)), false
}

// mkdir 建目录（含父级）。
func mkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFsListExcludesDotAndReservedNames(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	seedMixedDir(t, root)
	addStorage(t, "/media", "Local", root)

	names, total, nullContent := listPage(t, e, "/media", "")
	if nullContent {
		t.Fatal("content 不该是 null，空列表也应是 []")
	}

	// 逐项断言（刻意不依赖下面拼期望值的逻辑，避免同一个 bug 两边一起错）
	for _, bad := range excludedEntries() {
		if slices.Contains(names, bad) {
			t.Errorf("应被排除的 %q 仍被下发，got %v", bad, names)
		}
	}
	for _, good := range append(slices.Clone(visibleEntries), visibleDirs...) {
		if !slices.Contains(names, good) {
			t.Errorf("可见项 %q 被误伤过滤掉了，got %v", good, names)
		}
	}

	// 展示顺序：目录 → 视频 → 音频 → 图片 → 文本 → 其它，每组内按忽略大小写的名字序。
	// 这里把期望顺序**硬编码**出来，不复刻实现里的排序规则 —— 实现改错时两边才会一起错。
	// 未加挂载点（只挂了 /media），所以这一层没有虚拟挂载点条目。
	want := []string{
		// 目录（sub 与 sub2 也是唯一两个目录）
		"sub", "sub2",
		// 视频 .mp4
		"#1 Hits.mp4", "$RECYCLE.BIN.mp4", "%off.mp4", "&amp;tag.mp4", "*待定.mp4",
		"?what.mp4", "^caret.mp4", "a#b.mp4", "C++入门.mp4", "movie.mp4", "zz.mp4",
		"爱在黎明破晓前.mp4", "神偷奶爸！.mp4",
		// 音频 .mp3
		"!important.mp3",
		// 图片 .png
		"@2x.png",
		// 文本 .txt
		"System Volume Information.txt",
		// 其它：无扩展名与未知扩展名
		"lost+found2", "Thumbs.db.bak",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("列表内容或顺序不符\n got = %v\nwant = %v", names, want)
	}

	// total 必须与过滤后的 content 一致，不能还按原始条数报
	if total != len(want) || total != len(names) {
		t.Errorf("total = %d, want %d（= content 条数 %d）", total, len(want), len(names))
	}
}

// TestExcludedNamesAreCaseInsensitive 系统保留名不区分大小写：
// 真实盘上 `$RECYCLE.BIN`/`Thumbs.db` 的大小写写法并不统一。
func TestExcludedNamesAreCaseInsensitive(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.mp4"), []byte("x"))
	// 注意 macOS 默认大小写不敏感，同一目录里不能同时放只差大小写的两个名字
	writeFile(t, filepath.Join(root, "$Recycle.Bin"), []byte("x"))
	writeFile(t, filepath.Join(root, "THUMBS.DB"), []byte("x"))
	mkdir(t, filepath.Join(root, "system volume information"))
	addStorage(t, "/media", "Local", root)

	names, total, _ := listPage(t, e, "/media", "")
	if !slices.Contains(names, "ok.mp4") {
		t.Errorf("可见项不该被过滤，got %v", names)
	}
	for _, bad := range []string{"$Recycle.Bin", "THUMBS.DB", "system volume information"} {
		if slices.Contains(names, bad) {
			t.Errorf("保留名 %q 不区分大小写，不该下发，got %v", bad, names)
		}
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
}

// TestFsListExcludesInSubdir 子目录里照同样规则过滤。
func TestFsListExcludesInSubdir(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "sub"))
	writeFile(t, filepath.Join(root, "sub", "inside.mp4"), []byte("x"))
	writeFile(t, filepath.Join(root, "sub", "$RECYCLE.BIN"), []byte("x"))
	addStorage(t, "/media", "Local", root)

	names, total, _ := listPage(t, e, "/media/sub", "")
	if !slices.Contains(names, "inside.mp4") {
		t.Errorf("子目录的可见文件应保留，got %v", names)
	}
	if slices.Contains(names, "$RECYCLE.BIN") {
		t.Errorf("子目录的保留名应被过滤，got %v", names)
	}
	if total != 1 || len(names) != 1 {
		t.Errorf("子目录应只剩 1 项，got total=%d names=%v", total, names)
	}
}

// TestFsListAllExcludedGivesEmptyArray 整个目录只有排除项时返回 []，不是 null。
func TestFsListAllExcludedGivesEmptyArray(t *testing.T) {
	e := newApp(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".only"), []byte("x"))
	writeFile(t, filepath.Join(root, "desktop.ini"), []byte("x"))
	mkdir(t, filepath.Join(root, "$RECYCLE.BIN"), filepath.Join(root, ".mocca"))
	addStorage(t, "/media", "Local", root)

	names, total, nullContent := listPage(t, e, "/media", "")
	if nullContent {
		t.Fatal("content 应为 []，不能是 null")
	}
	if len(names) != 0 || total != 0 {
		t.Errorf("应返回空列表，got names=%v total=%d", names, total)
	}
}
