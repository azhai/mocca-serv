//go:build unix

package mq

import (
	"path/filepath"
	"testing"
)

func TestShmPublishReadNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ring.shm")
	s, err := OpenShm(path, ShmOptions{SlotCount: 8, SlotSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	r := s.Subscribe("t", false) // 追新
	if _, ok := r.Next(); ok {
		t.Fatal("订阅后没发布不该有消息")
	}
	if err := s.Publish("t", "hello"); err != nil {
		t.Fatal(err)
	}
	m, ok := r.Next()
	if !ok || m.Topic != "t" || string(m.Body) != "hello" {
		t.Fatalf("读到的不对: %+v %v", m, ok)
	}
	if m.ID != 0 {
		t.Fatalf("首条 seq = %d, want 0", m.ID)
	}
}

func TestShmFromOldestReplays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ring.shm")
	s, _ := OpenShm(path, ShmOptions{SlotCount: 8, SlotSize: 256})
	defer s.Close()
	for _, v := range []string{"a", "b", "c"} {
		_ = s.Publish("t", v)
	}
	r := s.Subscribe("t", true) // 从最旧
	var got []string
	for {
		m, ok := r.Next()
		if !ok {
			break
		}
		got = append(got, string(m.Body))
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("重放 = %v, want [a b c]", got)
	}
}

func TestShmTopicFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ring.shm")
	s, _ := OpenShm(path, ShmOptions{SlotCount: 8, SlotSize: 256})
	defer s.Close()
	_ = s.Publish("a", "1")
	_ = s.Publish("b", "2")
	_ = s.Publish("a", "3")
	r := s.Subscribe("a", true)
	var got []string
	for {
		m, ok := r.Next()
		if !ok {
			break
		}
		got = append(got, string(m.Body))
	}
	if len(got) != 2 || got[0] != "1" || got[1] != "3" {
		t.Fatalf("过滤 = %v, want [1 3]", got)
	}
}

func TestShmSlowReaderSkips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ring.shm")
	s, _ := OpenShm(path, ShmOptions{SlotCount: 4, SlotSize: 128})
	defer s.Close()
	r := s.Subscribe("t", false)
	// 写满超过环容量：读者掉队，应跳过并计数，最终读到最新的若干条且不 panic。
	for i := 0; i < 10; i++ {
		_ = s.Publish("t", i)
	}
	seen := 0
	for {
		_, ok := r.Next()
		if !ok {
			break
		}
		seen++
	}
	if r.Dropped() == 0 {
		t.Fatal("掉队应计入 Dropped")
	}
	if seen == 0 {
		t.Fatal("掉队后仍应读到未被覆盖的消息")
	}
}

func TestShmTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ring.shm")
	s, _ := OpenShm(path, ShmOptions{SlotCount: 4, SlotSize: 32})
	defer s.Close()
	if err := s.Publish("t", make([]byte, 1000)); err != ErrShmTooLarge {
		t.Fatalf("超长应 ErrShmTooLarge, got %v", err)
	}
}

func TestShmReopenSharesRing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ring.shm")
	s1, _ := OpenShm(path, ShmOptions{SlotCount: 8, SlotSize: 256})
	_ = s1.Publish("t", "persisted")
	s1.Close()

	// 重开同一文件：应识别既有头部，读到之前写入的（fromOldest）。
	s2, err := OpenShm(path, ShmOptions{SlotCount: 8, SlotSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	r := s2.Subscribe("t", true)
	m, ok := r.Next()
	if !ok || string(m.Body) != "persisted" {
		t.Fatalf("重开应续用同一环: %+v %v", m, ok)
	}
}
