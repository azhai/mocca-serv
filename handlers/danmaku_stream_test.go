package handlers_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/azhai/mocca/models"
)

// danmakuFixtures 造一个能玩弹幕的最小环境：一个存储、一个视频、一份带 sha1 的索引。
func danmakuFixtures(t *testing.T) (root, filePath string) {
	t.Helper()
	root = contentRoot(t)
	addStorage(t, "/m", "Local", root)
	writeTestFile(t, root, "s.mp4", "video-data")
	writeTestFile(t, root, ".index.jsonl",
		`{"name":"s.mp4","size_kb":1,"modified":"2026-01-01T00:00:00Z","sha1":"`+
			strings.Repeat("cd", 20)+`","is_new":0}`+"\n")
	return root, "/m/s.mp4"
}

// TestDanmakuStreamPushes 发出去的弹幕要能实时推给正在看的人。
//
// 这是「实时互动」的核心：SSE 通道建立 → 另一个人发弹幕 → 这条弹幕出现在流里。
// 这里用**真实 HTTP 服务**而不是 ResponseRecorder：SSE 本来就是长连接 + 跨 goroutine，
// 而 Recorder 不是并发安全的（用它会报数据竞争，测的还只是测试代码自己的问题）。
func TestDanmakuStreamPushes(t *testing.T) {
	e := newApp(t)
	srv := httptest.NewServer(e)
	defer srv.Close()
	_, filePath := danmakuFixtures(t)
	tok := makeUser(t, e, "watcher", "p", models.RoleGeneral)

	req, err := http.NewRequest(http.MethodGet,
		srv.URL+"/api/danmaku/stream?path="+url.QueryEscape(filePath)+"&token="+tok, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Do 在响应头到达时就返回，后面是流式读
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("建立 SSE 连接失败: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("SSE 的 Cache-Control = %q, want no-cache", cc)
	}
	if ab := resp.Header.Get("X-Accel-Buffering"); ab != "no" {
		t.Errorf("应关掉反代的缓冲（否则弹幕会攒着一起到），got %q", ab)
	}

	// 另一个人发一条弹幕（走真实接口，不是直接调发布函数）
	sender := makeUser(t, e, "sender", "p", models.RoleGeneral)
	if code, r := call(t, e, http.MethodPost, "/api/danmaku",
		`{"path":"`+filePath+`","offset":42000,"content":"实时的"}`, sender); code != 200 {
		t.Fatalf("发弹幕失败，got %d msg=%v", code, r["message"])
	}

	// 只在这一个 goroutine 里读流，读到的帧写进 channel 给断言用
	frames := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			frames <- sc.Text()
		}
		close(frames)
	}()

	deadline := time.After(3 * time.Second)
	var got []string
	for {
		select {
		case line, ok := <-frames:
			if !ok {
				t.Fatalf("流被提前关闭，收到: %v", got)
			}
			got = append(got, line)
			if strings.Contains(line, "实时的") {
				if !strings.Contains(strings.Join(got, "\n"), "event: danmaku") {
					t.Errorf("推送应带 event 名，收到: %v", got)
				}
				if !strings.Contains(line, `"offset":42000`) {
					t.Errorf("推送应带时间点，收到: %s", line)
				}
				// 断开：客户端关掉响应体，服务端 handler 必须跟着退出
				_ = resp.Body.Close()
				closed := make(chan struct{})
				go func() { srv.Close(); close(closed) }()
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					t.Fatal("客户端断开后 SSE handler 没退出（订阅与 goroutine 会一直堆着）")
				}
				return
			}
		case <-deadline:
			t.Fatalf("没等到弹幕推送，收到: %v", got)
		}
	}
}

// TestDanmakuStreamRejectsBadPath 路径不对 / 拿不到 sha1 时就别开流：按普通错误返回。
func TestDanmakuStreamRejectsBadPath(t *testing.T) {
	e := newApp(t)
	_, filePath := danmakuFixtures(t)
	tok := makeUser(t, e, "watcher2", "p", models.RoleGeneral)

	if code, _ := call(t, e, http.MethodGet, "/api/danmaku/stream", "", tok); code != 400 {
		t.Errorf("缺 path 应 400，got %d", code)
	}
	if code, _ := call(t, e, http.MethodGet, "/api/danmaku/stream?path=/m/none.mp4", "", tok); code != 400 {
		t.Errorf("拿不到 sha1 的媒体应 400，got %d", code)
	}

	// 正常路径确实能开流（连上就关，只为确认不是靠错误路径"通过"的）
	srv := httptest.NewServer(e)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/danmaku/stream?path=" + url.QueryEscape(filePath) + "&token=" + tok)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
		t.Fatalf("正常路径应能开流，got %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}
