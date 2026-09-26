package mediaindex_test

import (
	"sync"
	"testing"
	"time"

	"github.com/azhai/mocca/mediaindex"
)

// TestDanmakuHubPublishSubscribe 发布订阅：订阅者收到、没订阅的收不到、取消后不再收。
func TestDanmakuHubPublishSubscribe(t *testing.T) {
	const storage, sha = "disk-a", "aaaa"

	ch, cancel := mediaindex.SubscribeDanmaku(storage, sha)
	other, cancelOther := mediaindex.SubscribeDanmaku(storage, "bbbb") // 另一个视频
	defer cancelOther()

	e := &mediaindex.Entry{Content: "来了"}
	mediaindex.PublishDanmaku(storage, sha, e)

	select {
	case got := <-ch:
		if got.Content != "来了" {
			t.Errorf("收到的不对: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者没收到弹幕")
	}
	select {
	case got := <-other:
		t.Errorf("另一个视频的订阅者不该收到: %+v", got)
	default:
	}

	// 取消后不再收到
	cancel()
	mediaindex.PublishDanmaku(storage, sha, &mediaindex.Entry{Content: "之后"})
	select {
	case got := <-ch:
		t.Errorf("取消后还能收到: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestDanmakuHubSlowSubscriberDoesNotBlock 订阅者处理慢（缓冲满）时，绝不能拖住发送方。
//
// 弹幕是"丢了不心疼、卡住才是事故"的数据：一个卡死的客户端不能让所有人的弹幕都发不出去。
func TestDanmakuHubSlowSubscriberDoesNotBlock(t *testing.T) {
	const storage, sha = "disk-b", "cccc"
	_, cancel := mediaindex.SubscribeDanmaku(storage, sha) // 订阅了但一直不读
	defer cancel()

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
}

// TestDanmakuHubConcurrent 并发订阅/取消/发布不能崩（-race 下才有意义）。
func TestDanmakuHubConcurrent(t *testing.T) {
	const storage, sha = "disk-c", "dddd"
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := mediaindex.SubscribeDanmaku(storage, sha)
			mediaindex.PublishDanmaku(storage, sha, &mediaindex.Entry{Content: "x"})
			select {
			case <-ch:
			default:
			}
			cancel()
		}()
	}
	wg.Wait()
}
