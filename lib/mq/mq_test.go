package mq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	f := encodeFrame("danmaku", []byte(`{"text":"hi"}`))
	topic, body, ok := decodeFrame(f)
	if !ok || topic != "danmaku" || string(body) != `{"text":"hi"}` {
		t.Fatalf("frame round-trip failed: %q %q %v", topic, body, ok)
	}
	if _, _, ok := decodeFrame(f[:1]); ok {
		t.Fatal("短于 2 字节应解码失败")
	}
}

func TestBusPublishSubscribe(t *testing.T) {
	b := New(Options{})
	defer b.Close()

	sub, err := b.Subscribe("chat")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Subscribers(); got != 1 {
		t.Fatalf("Subscribers = %d, want 1", got)
	}

	// 结构体：一次编码，多读者共享。
	if n, err := b.Publish("chat", map[string]any{"u": "alice"}); err != nil || n != 1 {
		t.Fatalf("Publish = (%d,%v)", n, err)
	}
	m, ok := sub.Next()
	if !ok {
		t.Fatal("没取到消息")
	}
	if m.Topic != "chat" || string(m.Body) != `{"u":"alice"}` {
		t.Fatalf("消息不对: %+v", m)
	}

	// 别的 topic 不该收到。
	if n, _ := b.Publish("other", "x"); n != 0 {
		t.Fatalf("other topic 命中了 %d 个订阅者", n)
	}

	// string / []byte 原样字节，不再包一层 JSON。
	if _, err := b.Publish("chat", "raw"); err != nil {
		t.Fatal(err)
	}
	m, _ = sub.Next()
	if string(m.Body) != "raw" {
		t.Fatalf("string 应原样: %q", m.Body)
	}
}

func TestBusEncodeOnceSharedByMany(t *testing.T) {
	b := New(Options{})
	defer b.Close()
	s1, _ := b.Subscribe("t")
	s2, _ := b.Subscribe("t")
	if n, _ := b.Publish("t", []byte("payload")); n != 2 {
		t.Fatalf("应有 2 个订阅者, got %d", n)
	}
	m1, _ := s1.Next()
	m2, _ := s2.Next()
	if string(m1.Body) != "payload" || string(m2.Body) != "payload" {
		t.Fatal("扇出失败")
	}
	if m1.ID != m2.ID {
		t.Fatalf("同一消息 ID 应一致: %d vs %d", m1.ID, m2.ID)
	}
}

func TestBusDropNewestCounts(t *testing.T) {
	b := New(Options{Backlog: 1}) // 只缓冲一条
	defer b.Close()
	sub, _ := b.Subscribe("t")
	for i := 0; i < 5; i++ {
		_, _ = b.Publish("t", i)
	}
	if d := sub.Dropped(); d != 4 {
		t.Fatalf("Dropped = %d, want 4", d)
	}
	m, ok := sub.Next() // 只有第一条进得去
	if !ok || string(m.Body) != "0" {
		t.Fatalf("应读到第一条: %+v %v", m, ok)
	}
}

func TestBusCancelReleasesSlot(t *testing.T) {
	b := New(Options{MaxSubs: 2})
	defer b.Close()
	s1, _ := b.Subscribe("a")
	if _, err := b.Subscribe("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Subscribe("c"); err != ErrTooManySubs {
		t.Fatalf("第 3 个应达上限, got %v", err)
	}
	s1.Cancel()
	s1.Cancel() // 幂等
	if _, err := b.Subscribe("c"); err != nil {
		t.Fatalf("取消后应能再订阅: %v", err)
	}
}

func TestBusCloseWakesNext(t *testing.T) {
	b := New(Options{})
	sub, _ := b.Subscribe("t")
	_ = b.Close()
	if _, ok := sub.Next(); ok {
		t.Fatal("Close 后 Next 应结束")
	}
	if _, err := b.Publish("t", 1); err != ErrClosed {
		t.Fatalf("Close 后 Publish 应 ErrClosed: %v", err)
	}
	_ = b.Close() // 幂等
}

func TestBusDrainBeforeEnd(t *testing.T) {
	// Cancel 前已投递的消息，Cancel 后仍应被读空，再返回 false。
	b := New(Options{})
	defer b.Close()
	sub, _ := b.Subscribe("t")
	_, _ = b.Publish("t", "kept")
	sub.Cancel()
	if m, ok := sub.Next(); !ok || string(m.Body) != "kept" {
		t.Fatalf("存量应被读空: %+v %v", m, ok)
	}
	if _, ok := sub.Next(); ok {
		t.Fatal("读空后应结束")
	}
}

func TestPublishJSON(t *testing.T) {
	b := New(Options{})
	defer b.Close()
	sub, _ := b.Subscribe("t")
	raw := json.RawMessage(`{"a":1}`)
	if _, err := b.PublishJSON("t", raw); err != nil {
		t.Fatal(err)
	}
	m, _ := sub.Next()
	if string(m.Body) != `{"a":1}` {
		t.Fatalf("PublishJSON 原样: %q", m.Body)
	}
}

func TestJournalAppendRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.log")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := j.Append("idx", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	_ = j.Sync()

	r, err := OpenJournalReader(path, "idx")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	count := 0
	for {
		m, ok := r.Next()
		if !ok {
			break
		}
		if uint64(count) != m.ID {
			t.Fatalf("seq 不连续: got %d want %d", m.ID, count)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("读到 %d 条, want 3", count)
	}
	j.Close()
}

func TestJournalTopicFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.log")
	j, _ := OpenJournal(path)
	_, _ = j.Append("a", "1")
	_, _ = j.Append("b", "2")
	_, _ = j.Append("a", "3")
	j.Close()

	r, _ := OpenJournalReader(path, "a")
	defer r.Close()
	var got []string
	for {
		m, ok := r.Next()
		if !ok {
			break
		}
		got = append(got, string(m.Body))
	}
	if len(got) != 2 || got[0] != "1" || got[1] != "3" {
		t.Fatalf("过滤后 = %v, want [1 3]", got)
	}
}

func TestJournalReopenContinuesSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.log")
	j, _ := OpenJournal(path)
	s0, _ := j.Append("t", "x")
	j.Close()

	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := j2.Append("t", "y")
	j2.Close()
	if s0 != 0 || s1 != 1 {
		t.Fatalf("重开后 seq 应续上: %d %d", s0, s1)
	}
}

func TestJournalSeeksToSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.log")
	j, _ := OpenJournal(path)
	for i := 0; i < 5; i++ {
		_, _ = j.Append("t", i)
	}
	j.Close()

	r, _ := OpenJournalReader(path, "")
	defer r.Close()
	if err := r.SeekTo(3); err != nil {
		t.Fatal(err)
	}
	var ids []uint64
	for {
		m, ok := r.Next()
		if !ok {
			break
		}
		ids = append(ids, m.ID)
	}
	if len(ids) != 2 || ids[0] != 3 || ids[1] != 4 {
		t.Fatalf("SeekTo(3) 后应读到 [3 4], got %v", ids)
	}
}

func TestJournalTruncatesTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.log")
	j, _ := OpenJournal(path)
	_, _ = j.Append("t", "good")
	j.Close()

	// 手工追加半条脏记录。
	if err := appendGarbage(path); err != nil {
		t.Fatal(err)
	}
	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	// 重开后应能从干净处继续编号（脏尾被截断，不污染计数）。
	seq, _ := j2.Append("t", "after")
	if seq != 1 {
		t.Fatalf("脏尾截断后 seq = %d, want 1", seq)
	}
}

func TestEncodeBodyNil(t *testing.T) {
	b, err := encodeBody(nil)
	if err != nil || b != nil {
		t.Fatalf("nil 应编成空: %v %v", b, err)
	}
}

// appendGarbage 往文件尾写一条长度头谎报的半记录，模拟崩溃残留。
func appendGarbage(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(tornRecord)
	return err
}

var tornRecord = []byte{
	0, 0, 0, 0, 0, 0, 0, 0, // seq = 0
	0xff, 0xff, 0xff, 0x7f, // frameLen 谎报很大
	'x', 'x',
}

func TestJournalReaderNonBlocking(t *testing.T) {
	// 防呆：跨进程侧的 JournalReader.Next 到文件末尾时立即返回，不阻塞（poll 语义）。
	// Bus.Subscription.Next 是阻塞的（给 SSE 写循环用），两者语义不同，这里验后者。
	path := filepath.Join(t.TempDir(), "q.log")
	j, _ := OpenJournal(path)
	j.Close()
	r, _ := OpenJournalReader(path, "")
	defer r.Close()
	done := make(chan bool, 1)
	go func() {
		_, ok := r.Next()
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("空队列不应读到消息")
		}
	case <-time.After(time.Second):
		t.Fatal("Next 到末尾时不应阻塞")
	}
}
