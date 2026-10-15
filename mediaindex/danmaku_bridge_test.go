package mediaindex

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/azhai/mocca/lib/mq"
)

// 桥的三个关键性质，各一个测试，全部用 newBridge + 手动 tailOnce 驱动，
// 不依赖后台 ticker，也不碰全局 danmakuBus/bridge（newBridge 收自己的 Bus，
// StartDanmakuBridge 才会写全局指针），所以与别的测试互不干扰。

const (
	bridgeTopic    = "disk-a\x00aaaa"
	bridgePeerFile = "danmaku-peer.journal" // 固定名，≠ 本实例自生成的 danmaku-<pid>-xxx.journal
)

// recvOne 在 d 内尝试取一条；超时返回 ok=false（避免注入没发生时测试挂死）。
func recvOne(t *testing.T, s *mq.Subscription, d time.Duration) (mq.Message, bool) {
	t.Helper()
	select {
	case m := <-s.C():
		return m, true
	case <-time.After(d):
		return mq.Message{}, false
	}
}

// TestBridgeInjectsPeerJournal 别的实例写进它自己的文件，本实例 tail 后注入本地 Bus。
func TestBridgeInjectsPeerJournal(t *testing.T) {
	dir := t.TempDir()
	bus := mq.New(mq.Options{Backlog: 8})
	b, err := newBridge(dir, bus)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	sub, err := bus.Subscribe(bridgeTopic)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()

	// 模拟另一个实例：先建好它的 journal 文件（此刻为空）。
	peer, err := mq.OpenJournal(filepath.Join(dir, bridgePeerFile))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	b.tailOnce() // 第一轮：发现 peer 文件 → 建游标（移到末尾），此时无新记录

	// peer 在本实例开始 tail 之后才发的"实时"弹幕，应被注入。
	body := []byte(`{"content":"hello"}`)
	if _, err := peer.Append(bridgeTopic, body); err != nil {
		t.Fatal(err)
	}

	b.tailOnce() // 第二轮：读到新记录 → 注入本地 Bus

	m, ok := recvOne(t, sub, time.Second)
	if !ok {
		t.Fatal("peer 写的弹幕没被桥注入本地 Bus")
	}
	if m.Topic != bridgeTopic {
		t.Errorf("topic 应原样透传，得到 %q", m.Topic)
	}
	if string(m.Body) != string(body) {
		t.Errorf("body 应原样透传，得到 %q", m.Body)
	}
}

// TestBridgeNoEcho 本实例自己发的（写进自己的文件）不会从自己的文件读回来重复注入。
func TestBridgeNoEcho(t *testing.T) {
	dir := t.TempDir()
	bus := mq.New(mq.Options{Backlog: 8})
	b, err := newBridge(dir, bus)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	sub, _ := bus.Subscribe(bridgeTopic)
	defer sub.Cancel()

	// 出站：落到本实例自己的文件（tail 时应被 name==b.self 跳过）。
	if err := b.publishOut(bridgeTopic, []byte(`{"content":"mine"}`)); err != nil {
		t.Fatal(err)
	}

	b.tailOnce()

	if _, ok := recvOne(t, sub, 100*time.Millisecond); ok {
		t.Fatal("自己发的弹幕被读回来了（回环）——应跳过本实例文件")
	}
}

// TestBridgeNoReplay 首次发现某文件时把游标移到末尾：之前已在文件里的旧弹幕不重放，只投新消息。
func TestBridgeNoReplay(t *testing.T) {
	dir := t.TempDir()
	bus := mq.New(mq.Options{Backlog: 8})
	b, err := newBridge(dir, bus)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// 先让 peer 文件里已有"旧"记录（本实例启动前它就存在）。
	peer, err := mq.OpenJournal(filepath.Join(dir, bridgePeerFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Append(bridgeTopic, []byte(`{"content":"old"}`)); err != nil {
		t.Fatal(err)
	}

	sub, _ := bus.Subscribe(bridgeTopic)
	defer sub.Cancel()

	b.tailOnce() // 此刻才发现 peer 文件 → 游标移到末尾 → 旧的不应投
	if _, ok := recvOne(t, sub, 100*time.Millisecond); ok {
		t.Fatal("桥重放了发现之前就已存在的旧弹幕，应只投新消息")
	}

	// 之后再追一条"新"的，下一轮应投出。
	if _, err := peer.Append(bridgeTopic, []byte(`{"content":"new"}`)); err != nil {
		t.Fatal(err)
	}
	_ = peer.Close()

	b.tailOnce()
	m, ok := recvOne(t, sub, time.Second)
	if !ok {
		t.Fatal("发现之后新追的弹幕没被注入")
	}
	if string(m.Body) != `{"content":"new"}` {
		t.Errorf("应只收到新那条，得到 %q", m.Body)
	}
}
