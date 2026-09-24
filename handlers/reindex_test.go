package handlers_test

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
)

// TestFsReindexFixesStaleSHA1 「重新索引」必须把过期的 sha1 纠正回来，并让已有的封面重新可见。
//
// 这条守着一类只在真实环境出现、且极易误判的问题：海报与简介都按 sha1 寻址，而**增量扫描
// 在 size+modified 与旧行一致时会沿用旧 sha1**。文件被替换/改名、或还没下完就被扫过之后，
// 索引里那条 sha1 就是错的 —— 封面明明在磁盘上（就在真实 sha1 的目录下），/meta/poster
// 却怎么也找不到，用户看到的就是"刮削过却没有封面"。
//
// 这里刻意构造"索引里那条是**改动之前**的记录"（sha1 错、size/modified 也都是旧的），
// 与真实场景一致：只要文件真的被动过，按 size+modified 一比就能发现要重算。
func TestFsReindexFixesStaleSHA1(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)

	const content = "real-content"
	writeTestFile(t, root, "Movie.2020.mkv", content)
	sum := sha1.Sum([]byte(content))
	realSHA := hex.EncodeToString(sum[:])

	// 封面已经存在（就在真实 sha1 的目录下），索引里却写着另一个 sha1
	posterRel, err := mediaindex.PosterRel(realSHA)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, filepath.Join(".mocca", filepath.FromSlash(posterRel)), "USER-COVER")
	// 索引是"文件改动之前"留下的：名字对得上，size/modified/sha1 都是旧的
	writeTestFile(t, root, ".index.jsonl", fmt.Sprintf(
		`{"name":"Movie.2020.mkv","size_kb":%d,"modified":%q,"sha1":%q,"is_new":1}`+"\n",
		(len(content)+1023)/1024, "2020-01-01T00:00:00Z",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	poster := func() int {
		req := httptest.NewRequest(http.MethodGet, "/meta/poster?path=%2Fmedia%2FMovie.2020.mkv", nil)
		req.Header.Set("Authorization", admin)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}

	// 重算之前：封面在磁盘上，却因为索引里的 sha1 是错的而取不到
	if code := poster(); code != http.StatusNotFound {
		t.Fatalf("前置场景不成立：sha1 过期时 /meta/poster 应为 404，got %d", code)
	}

	code, resp := call(t, e, http.MethodPost, "/api/fs/reindex",
		`{"path":"/media/Movie.2020.mkv"}`, admin)
	if code != 200 {
		t.Fatalf("重新索引应成功，got %d msg=%v", code, resp["message"])
	}
	// 回包：1 个目录（索引按目录一份、整份重写）、1 个文件（被强制重算的数量）
	if d, _ := resp["data"].(map[string]any); d["dirs"] != float64(1) || d["files"] != float64(1) {
		t.Errorf("应报告重建 1 个目录 / 1 个视频: %v", d)
	}

	// 索引文件里也必须换成新值，否则下次读回还是找不到
	raw, err := os.ReadFile(filepath.Join(root, ".index.jsonl"))
	if err != nil {
		t.Fatalf("读索引失败: %v", err)
	}
	var rec struct {
		SHA1 string `json:"sha1"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.SHA1 != realSHA {
		t.Errorf("索引里的 sha1 没被纠正: got %s want %s", rec.SHA1, realSHA)
	}

	// 关键：用户能看到封面了
	if code := poster(); code != http.StatusOK {
		t.Errorf("重新索引后 /meta/poster 应可用，got %d", code)
	}
}

// TestFsReindexIncremental 重新索引是**增量重建**：按 size+modified 逐条判定，
// 文件没变就沿用旧行（不读文件内容），只有真的变了的才重算 sha1。
//
// 勾选只决定"刷新哪些目录"，不决定重算哪些文件。回包：`files` = 写进索引的记录数，
// `rehashed` = 其中真正重算 sha1 的条数。
func TestFsReindexIncremental(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)

	const wrong = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	names := []string{"One.2001.mkv", "Two.2002.mkv"}
	idxLines := make([]string, 0, len(names))
	for _, n := range names {
		writeTestFile(t, root, n, "content-"+n)
		st, err := os.Stat(filepath.Join(root, n))
		if err != nil {
			t.Fatal(err)
		}
		// size/modified 与真实一致、sha1 却是错的 —— 按增量判据就是"没变"，应原样沿用
		idxLines = append(idxLines, fmt.Sprintf(
			`{"name":%q,"size_kb":%d,"modified":%q,"sha1":%q,"is_new":1}`,
			n, (st.Size()+1023)/1024, st.ModTime().UTC().Format(time.RFC3339Nano), wrong))
	}
	writeTestFile(t, root, ".index.jsonl", strings.Join(idxLines, "\n")+"\n")
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	shaOf := func() map[string]string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, ".index.jsonl"))
		if err != nil {
			t.Fatalf("读索引失败: %v", err)
		}
		got := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var rec struct {
				Name string `json:"name"`
				SHA1 string `json:"sha1"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatal(err)
			}
			got[rec.Name] = rec.SHA1
		}
		return got
	}
	// 第一轮：两个文件都没动过 → 只写记录，一条都不重算
	code, resp := call(t, e, http.MethodPost, "/api/fs/reindex",
		`{"paths":["/media/One.2001.mkv","/media/Two.2002.mkv"]}`, admin)
	if code != 200 {
		t.Fatalf("重新索引应成功，got %d msg=%v", code, resp["message"])
	}
	if d, _ := resp["data"].(map[string]any); d["dirs"] != float64(1) ||
		d["files"] != float64(2) || d["rehashed"] != float64(0) {
		t.Errorf("文件都没变：应报告 1 个目录 / 2 条记录 / 0 条重算: %v", d)
	}
	got := shaOf()
	for _, n := range names {
		if got[n] != wrong {
			t.Errorf("%s 没被改动，sha1 不该被动: got %s", n, got[n])
		}
	}

	// 第二轮：改掉 One 的内容（size+modified 随之变化）→ 只有它重算
	writeTestFile(t, root, "One.2001.mkv", "content-One.2001.mkv-CHANGED")
	code, resp = call(t, e, http.MethodPost, "/api/fs/reindex",
		`{"paths":["/media/One.2001.mkv","/media/Two.2002.mkv"]}`, admin)
	if code != 200 {
		t.Fatalf("重新索引应成功，got %d msg=%v", code, resp["message"])
	}
	if d, _ := resp["data"].(map[string]any); d["dirs"] != float64(1) ||
		d["files"] != float64(2) || d["rehashed"] != float64(1) {
		t.Errorf("只改了 One：应报告 1 个目录 / 2 条记录 / 1 条重算: %v", d)
	}
	got = shaOf()
	sum := sha1.Sum([]byte("content-One.2001.mkv-CHANGED"))
	if wantSum := hex.EncodeToString(sum[:]); got["One.2001.mkv"] != wantSum {
		t.Errorf("改过的 One 应重算为 %s，got %s", wantSum, got["One.2001.mkv"])
	}
	if got["Two.2002.mkv"] != wrong {
		t.Errorf("没动的 Two 不该被重算: got %s", got["Two.2002.mkv"])
	}
}

// TestFsReindexRejectsBadInput 边界：一个路径都没给要 400，路径不存在要 404。
// 用 callRaw 而不是 call —— 后者把"非 200"直接当失败，这里恰恰要断言非 200。
func TestFsReindexRejectsBadInput(t *testing.T) {
	e := newApp(t)
	root := contentRoot(t)
	addStorage(t, "/media", "Local", root)
	writeTestFile(t, root, "sub/keep.txt", "x")
	admin := makeUser(t, e, "root", "p", models.RoleAdmin)

	for _, tc := range []struct {
		body string
		want int
		name string
	}{
		{`{}`, http.StatusBadRequest, "一个路径都没给"},
		{`{"paths":[]}`, http.StatusBadRequest, "paths 为空"},
		{`{"path":"/media/none.mkv"}`, http.StatusNotFound, "文件不存在"},
	} {
		code, resp, _ := callRaw(t, e, http.MethodPost, "/api/fs/reindex", tc.body, admin)
		if code != tc.want {
			t.Errorf("%s 应 %d，got %d msg=%v", tc.name, tc.want, code, resp["message"])
		}
	}
}
