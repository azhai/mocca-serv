package mediaindex_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/mediaindex"
)

// TestDanmakuLivesInMoccaDir 弹幕必须落在**设备侧的 .mocca 里**，不进数据库。
//
// 这条钉的是用户明确要求的落盘位置：像海报/简介那样按 sha1 分目录，
// 于是换机器、重装服务、把盘拔走，弹幕都跟着走。
func TestDanmakuLivesInMoccaDir(t *testing.T) {
	sha := sha1hex("video") // 40 位十六进制
	rel, err := mediaindex.DanmakuRel(sha)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(sha[0:2], sha[2:4], sha[4:]+".danmaku.jsonl")
	if rel != want {
		t.Errorf("弹幕文件相对路径 = %q, want %q", rel, want)
	}

	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()

	e := &mediaindex.Entry{
		UserID: 7, UserName: "小明", UserAvatar: "07",
		Type: mediaindex.DanmakuTypeDanmaku, Offset: 12000, Content: "前方高能",
	}
	if err := mediaindex.AppendDanmaku(drv, sha, e); err != nil {
		t.Fatalf("追加弹幕失败: %v", err)
	}
	p := filepath.Join(root, ".mocca", filepath.FromSlash(rel))
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("弹幕没落在 .mocca 下（%s）: %v", p, err)
	}
}

// TestDanmakuAppendAndRead 追加后能读回，字段完整，且**作者信息是快照**。
func TestDanmakuAppendAndRead(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	dm := &mediaindex.Entry{
		UserID: 1, UserName: "小明", UserAvatar: "07",
		Type: mediaindex.DanmakuTypeDanmaku, Offset: 3000, Content: "这段配乐绝了",
	}
	cm := &mediaindex.Entry{
		UserID: 2, UserName: "小红", UserAvatar: "03",
		Type: mediaindex.DanmakuTypeComment, Offset: 9999, Content: "整部都很棒",
	}
	for _, e := range []*mediaindex.Entry{dm, cm} {
		if err := mediaindex.AppendDanmaku(drv, sha, e); err != nil {
			t.Fatalf("追加失败: %v", err)
		}
		if e.ID == "" {
			t.Fatal("ID 应自动生成")
		}
	}

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		t.Fatalf("读弹幕失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应有 2 条，got %d", len(rows))
	}
	// 按时间序：先发的弹幕在前
	if rows[0].Type != mediaindex.DanmakuTypeDanmaku || rows[0].Offset != 3000 {
		t.Errorf("弹幕时间点应保持 3000: %+v", rows[0])
	}
	// 评论 = 时间点为 0 的弹幕：传入的 9999 应被归零
	if rows[1].Type != mediaindex.DanmakuTypeComment || rows[1].Offset != 0 {
		t.Errorf("评论的 offset 应归零: %+v", rows[1])
	}
	// 作者信息直接写在行里，读出来就能渲染，不用 join 用户表
	if rows[0].UserName != "小明" || rows[0].UserAvatar != "07" || rows[0].UserID != 1 {
		t.Errorf("作者快照丢失: %+v", rows[0])
	}
}

// TestDanmakuInRange 按时间轴取窗口（播放器只取当前片段用）。
func TestDanmakuInRange(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	for _, ms := range []int{1000, 5000, 9000, 20000} {
		if err := mediaindex.AppendDanmaku(drv, sha, &mediaindex.Entry{
			UserName: "u", Type: mediaindex.DanmakuTypeDanmaku, Offset: ms, Content: "x",
		}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		t.Fatal(err)
	}
	got := mediaindex.DanmakuInRange(rows, 5000, 10000)
	if len(got) != 2 {
		t.Fatalf("[5000,10000] 应有 2 条，got %d", len(got))
	}
	if got[0].Offset != 5000 || got[1].Offset != 9000 {
		t.Errorf("窗口内应按时刻排序: %d %d", got[0].Offset, got[1].Offset)
	}
	// 评论（offset=0）不该混进弹幕窗口
	if n := len(mediaindex.DanmakuInRange(rows, 0, 60000)); n != 4 {
		t.Errorf("评论不该算进弹幕窗口，got %d", n)
	}
}

// TestCommentThreads 评论能独立展示，并带一层回复。
func TestCommentThreads(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	top := &mediaindex.Entry{UserName: "甲", Type: mediaindex.DanmakuTypeComment, Content: "顶"}
	if err := mediaindex.AppendDanmaku(drv, sha, top); err != nil {
		t.Fatal(err)
	}
	reply := &mediaindex.Entry{
		UserName: "乙", Type: mediaindex.DanmakuTypeComment, Content: "回", ParentID: top.ID,
	}
	if err := mediaindex.AppendDanmaku(drv, sha, reply); err != nil {
		t.Fatal(err)
	}

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		t.Fatal(err)
	}
	threads := mediaindex.CommentThreads(rows)
	if len(threads) != 1 {
		t.Fatalf("应只有 1 条顶层评论（回复不该浮在外面），got %d", len(threads))
	}
	if threads[0].Content != "顶" || len(threads[0].Replies) != 1 {
		t.Fatalf("顶层/回复不对: %+v", threads[0])
	}
	if threads[0].Replies[0].Content != "回" || threads[0].Replies[0].UserName != "乙" {
		t.Errorf("回复内容或作者不对: %+v", threads[0].Replies[0])
	}
}

// TestCommentThreadsNewestFirst 顶层评论**倒序**（最新在最上面），回复保持正序。
//
// 用户要求"按撰写时间倒序展示评论"，并且填写框放在最上面 —— 进来先看到最新的那条。
// 回复不动：它是一段对话，顺着读才接得上。
func TestCommentThreadsNewestFirst(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	first := &mediaindex.Entry{UserName: "甲", Type: mediaindex.DanmakuTypeComment, Content: "先发的"}
	if err := mediaindex.AppendDanmaku(drv, sha, first); err != nil {
		t.Fatal(err)
	}
	// 给两条顶层评论留出不同的毫秒，否则比的只是"同毫秒内的序号"
	time.Sleep(2 * time.Millisecond)
	second := &mediaindex.Entry{UserName: "乙", Type: mediaindex.DanmakuTypeComment, Content: "后发的"}
	if err := mediaindex.AppendDanmaku(drv, sha, second); err != nil {
		t.Fatal(err)
	}
	// 两条回复挂在"后发的"下面，按其发生顺序正序
	for _, txt := range []string{"回一", "回二"} {
		if err := mediaindex.AppendDanmaku(drv, sha, &mediaindex.Entry{
			UserName: "丙", Type: mediaindex.DanmakuTypeComment, Content: txt, ParentID: second.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		t.Fatal(err)
	}
	threads := mediaindex.CommentThreads(rows)
	if len(threads) != 2 {
		t.Fatalf("应有 2 条顶层评论，got %d", len(threads))
	}
	if threads[0].Content != "后发的" || threads[1].Content != "先发的" {
		t.Errorf("顶层评论应倒序（最新在前），got %q, %q", threads[0].Content, threads[1].Content)
	}
	if reps := threads[0].Replies; len(reps) != 2 || reps[0].Content != "回一" || reps[1].Content != "回二" {
		t.Errorf("回复应保持正序，got %+v", reps)
	}
}

// TestDeleteDanmaku 只能删自己的；管理员可以删别人的；删光了文件也不留空壳。
func TestDeleteDanmaku(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	mine := &mediaindex.Entry{UserID: 1, UserName: "我", Type: mediaindex.DanmakuTypeComment, Content: "我的"}
	other := &mediaindex.Entry{UserID: 2, UserName: "别人", Type: mediaindex.DanmakuTypeComment, Content: "他的"}
	for _, e := range []*mediaindex.Entry{mine, other} {
		if err := mediaindex.AppendDanmaku(drv, sha, e); err != nil {
			t.Fatal(err)
		}
	}

	if err := mediaindex.DeleteDanmaku(drv, sha, other.ID, 1, false); err == nil {
		t.Error("不该能删别人的弹幕")
	}
	if err := mediaindex.DeleteDanmaku(drv, sha, other.ID, 1, true); err != nil {
		t.Errorf("管理员应能删: %v", err)
	}
	if err := mediaindex.DeleteDanmaku(drv, sha, "not-exist", 1, true); err == nil {
		t.Error("删不存在的应报错")
	}

	// 删光后文件不留空壳
	if err := mediaindex.DeleteDanmaku(drv, sha, mine.ID, 1, false); err != nil {
		t.Fatalf("删自己的应成功: %v", err)
	}
	rel, _ := mediaindex.DanmakuRel(sha)
	if _, err := os.Stat(filepath.Join(root, ".mocca", filepath.FromSlash(rel))); err == nil {
		t.Error("删光后弹幕文件应被删除，不留空壳")
	}
	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil || len(rows) != 0 {
		t.Errorf("删光后应读到空: %v / %d 条", err, len(rows))
	}
}

// TestDanmakuConcurrentAppend 高并发发送：一条都不能丢、不能串行。
//
// 这是「高并发稳定读写」的核心断言。追加写本身是原子的，但「确认末尾换行 + 追加」
// 这两步若不加锁就会拼出坏行（见 danmakuMu 的注释）。不加锁时这条会失败。
func TestDanmakuConcurrentAppend(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	const n = 60
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- mediaindex.AppendDanmaku(drv, sha, &mediaindex.Entry{
				UserID: uint(i), UserName: "u", UserAvatar: "01",
				Type: mediaindex.DanmakuTypeDanmaku, Offset: i * 1000, Content: "弹幕",
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发追加失败: %v", err)
		}
	}

	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != n {
		t.Fatalf("并发写了 %d 条，只读到 %d 条（有丢失或写坏了行）", n, len(rows))
	}
	seen := map[int]bool{}
	for _, r := range rows {
		if r.Content != "弹幕" {
			t.Errorf("出现了坏行: %+v", r)
		}
		seen[r.Offset/1000] = true
	}
	if len(seen) != n {
		t.Errorf("只有 %d 个不同时刻，应 %d", len(seen), n)
	}
}

// TestDanmakuBrokenTailHeals 上一次写被打断留下的半行，不能污染后续记录。
func TestDanmakuBrokenTailHeals(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	good := &mediaindex.Entry{UserName: "u", Type: mediaindex.DanmakuTypeComment, Content: "第一条"}
	if err := mediaindex.AppendDanmaku(drv, sha, good); err != nil {
		t.Fatal(err)
	}
	// 手工模拟"写到一半"：追加一段没有换行的残缺 JSON
	rel, _ := mediaindex.DanmakuRel(sha)
	f, err := drv.MetaAppend(rel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(`{"id":"half","content":"半行`)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mediaindex.InvalidateDanmakuCache(drv, sha)

	after := &mediaindex.Entry{UserName: "u", Type: mediaindex.DanmakuTypeComment, Content: "第二条"}
	if err := mediaindex.AppendDanmaku(drv, sha, after); err != nil {
		t.Fatalf("半行之后应仍能追加: %v", err)
	}
	rows, err := mediaindex.ReadDanmaku(drv, sha)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("半行应被丢弃、两条正常记录都应读到，got %d: %+v", len(rows), rows)
	}
	if rows[0].Content != "第一条" || rows[1].Content != "第二条" {
		t.Errorf("记录内容不对: %+v", rows)
	}
}

// TestDanmakuLengthLimit 长度上限是并发安全的前提（不是体验限制）。
func TestDanmakuLengthLimit(t *testing.T) {
	root := t.TempDir()
	drv := openDriver(t, root)
	defer func() { _ = drv.Close() }()
	sha := sha1hex("video")

	long := strings.Repeat("字", mediaindex.MaxDanmakuLength+1)
	if err := mediaindex.AppendDanmaku(drv, sha, &mediaindex.Entry{
		Type: mediaindex.DanmakuTypeDanmaku, Offset: 1, Content: long,
	}); err == nil {
		t.Error("超长弹幕应被拒（超了就无法保证原子追加）")
	}
	if err := mediaindex.AppendDanmaku(drv, sha, &mediaindex.Entry{
		Type: mediaindex.DanmakuTypeComment, Content: strings.Repeat("字", mediaindex.MaxCommentLength+1),
	}); err == nil {
		t.Error("超长评论应被拒")
	}
	if err := mediaindex.AppendDanmaku(drv, sha, &mediaindex.Entry{
		Type: mediaindex.DanmakuTypeDanmaku, Offset: -1, Content: "x",
	}); err == nil {
		t.Error("负时间点应被拒")
	}
}

// openDriverIfNeeded 让本文件不依赖 scan_test.go 之外的东西。
var _ = drivers.Driver(nil)
