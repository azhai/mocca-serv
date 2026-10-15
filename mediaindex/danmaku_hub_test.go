package mediaindex_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/azhai/mocca/mediaindex"
)

// entryOf 把订阅收到的一条 Message.Body 解回 Entry（弹幕总线只走字节）。
func entryOf(t *testing.T, body []byte) mediaindex.Entry {
	t.Helper()
	var e mediaindex.Entry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("弹幕 Body 不是合法 JSON: %v（原始: %q）", err, body)
	}
	return e
}

// TestDanmakuHubPublishSubscribe 发布订阅：订阅者收到、没订阅的收不到、取消后不再收。
func TestDanmakuHubPublishSubscribe(t *testing.T) {
	const storage, sha = "disk-a", "aaaa"

	sub, err := mediaindex.SubscribeDanmaku(storage, sha)
	if err != nil {
		t.Fatal(err)
	}
	other, err := mediaindex.SubscribeDanmaku(storage, "bbbb") // 另一个视频
	if err != nil {
		t.Fatal(err)
	}
	defer other.Cancel()

	mediaindex.PublishDanmaku(storage, sha, &mediaindex.Entry{Content: "来了"})

	select {
	case m := <-sub.C():
		if got := entryOf(t, m.Body); got.Content != "来了" {
			t.Errorf("收到的不对: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者没收到弹幕")
	}
	select {
	case m := <-other.C():
		t.Errorf("另一个视频的订阅者不该收到: %q", m.Body)
	default:
	}

	// 取消后不再收到。
	sub.Cancel()
	mediaindex.PublishDanmaku(storage, sha, &mediaindex.Entry{Content: "之后"})
	if _, ok := sub.Next(); ok {
		t.Error("取消后还能收到")
	}
}

// TestDanmakuHubSlowSubscriberDoesNotBlock 订阅者处理慢（缓冲满）时，绝不能拖住发送方。
//
// 弹幕是"丢了不心疼、卡住才是事故"的数据：一个卡死的客户端不能让所有人的弹幕都发不出去。
func TestDanmakuHubSlowSubscriberDoesNotBlock(t *testing.T) {
	const storage, sha = "disk-b", "cccc"
	sub, err := mediaindex.SubscribeDanmaku(storage, sha) // 订阅了但一直不读
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ { // 远超缓冲
			mediaindex.PublishDanmaku(storage, sha, &mediaindex.Entry{Content: "刷屏"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("发布被慢订阅者阻塞了")
	}
	// 满了按 DropNewest 丢，计数应可查（说明总线确实挡住了而不是无声吞掉）。
	if sub.Dropped() == 0 {
		t.Error("慢订阅者应有丢弃计数")
	}
}

// TestDanmakuHubConcurrent 并发订阅/取消/发布不能崩（-race 下才有意义）。
func TestDanmakuHubConcurrent(t *testing.T) {
	const storage, sha = "disk-c", "dddd"
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, err := mediaindex.SubscribeDanmaku(storage, sha)
			if err != nil {
				return
			}
			mediaindex.PublishDanmaku(storage, sha, &mediaindex.Entry{Content: "x"})
			select {
			case <-sub.C():
			default:
			}
			sub.Cancel()
		}()
	}
	wg.Wait()
}
