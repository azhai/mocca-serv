package mq

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
	"time"
)

// Journal 是跨进程的文件追加队列：一条记录一旦落盘就不动，读方各自带字节游标往前扫。
//
// 磁盘上的记录形状（在 frame 外面套一层定长头，让读方能自定位、能跳过不关心的 topic）：
//
//	[8B seq][4B frameLen][frame...]   // frame 见 frame.go
//
// 单写者模型：Append 内部用一把互斥锁串行化同一进程内的多次写；跨进程的「谁在写」
// 由调用方保证（一个文件一个写者）。多写者会交错写坏记录头，这里不做文件锁 —— 与
// 现有唯一消费者（索引增量通知：一个索引协程产、若干 SSE 协程消费）的形状对齐。
//
// 为什么不用现成的队列中间件：这只服务本机进程间，Journal 提供「追加 + 游标读」，
// Shm（unix）提供「不落盘的定长环」。慢读者只会掉自己的消息，绝不影响写者。
type Journal struct {
	f    *os.File
	mu   sync.Mutex
	next uint64 // 下一条要写的 seq
}

// OpenJournal 打开（或创建）一个追加队列文件。
// 已存在的文件按末尾继续编号；空文件从 0 开始。
func OpenJournal(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	// 扫到最后一条 seq，续上编号。半条脏记录（上次崩溃留下）在此截断。
	j := &Journal{f: f}
	next, clean := scanJournal(f, size, 0)
	if clean != size {
		if err := f.Truncate(clean); err != nil {
			_ = f.Close()
			return nil, err
		}
		if _, err := f.Seek(clean, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	j.next = next
	return j, nil
}

// Append 追加一条消息，返回它的 seq。v 的编码规则同 Bus.Publish。
func (j *Journal) Append(topic string, v any) (uint64, error) {
	body, err := encodeBody(v)
	if err != nil {
		return 0, err
	}
	frame := encodeFrame(topic, body)
	hdr := make([]byte, 12)
	j.mu.Lock()
	seq := j.next
	binary.LittleEndian.PutUint64(hdr, seq)
	binary.LittleEndian.PutUint32(hdr[8:], uint32(len(frame)))
	_, err = j.f.Write(append(hdr, frame...))
	if err != nil {
		j.mu.Unlock()
		return 0, err
	}
	j.next = seq + 1
	j.mu.Unlock()
	return seq, nil
}

// Sync 把缓冲刷到磁盘（掉电不丢的最后一条边界；不常调，每次 Append 依赖 OS page cache）。
func (j *Journal) Sync() error { return j.f.Sync() }

// Close 关闭底层文件。
func (j *Journal) Close() error { return j.f.Close() }

// JournalReader 从 Journal 文件里按游标顺序读。可按 topic 过滤（在读者本地做）。
// 非阻塞语义：Next 读到文件末尾就返回 ok=false，调用方决定是 sleep-poll 还是退出。
type JournalReader struct {
	f         *os.File
	topic     string // 空串表示不过滤
	off       int64  // 下一个待读字节偏移
	lastSeq   uint64
	sizeCache int64 // 已知文件大小，省一次 Stat
}

// OpenJournalReader 打开一个队列文件用于读。topic 传空串读全部。
func OpenJournalReader(path, topic string) (*JournalReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &JournalReader{f: f, topic: topic, sizeCache: size}, nil
}

// SeekTo 把游标移到 seq >= want 的第一条记录。want 传 0 回到文件头（重放全部）。
// 从头线性扫：队列文件本就不大，且这是「重连后补历史」的低频操作。
func (r *JournalReader) SeekTo(want uint64) error {
	if want == 0 { // 重放全部：回到文件头
		r.off = 0
		_, err := r.f.Seek(0, io.SeekStart)
		return err
	}
	if _, err := r.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// 重新取当前长度：文件可能自打开后又追写了。
	if fi, err := r.f.Stat(); err == nil {
		r.sizeCache = fi.Size()
	}
	_, clean := scanJournal(r.f, r.sizeCache, want)
	r.off = clean
	_, err := r.f.Seek(r.off, io.SeekStart)
	return err
}

// Close 关闭读侧文件。
func (r *JournalReader) Close() error { return r.f.Close() }

// Next 读下一条（按 topic 过滤后的）消息。读到末尾返回 ok=false。
func (r *JournalReader) Next() (Message, bool) {
	for {
		hdr := make([]byte, 12)
		if _, err := r.f.ReadAt(hdr, r.off); err != nil {
			return Message{}, false // 不够一条头：还没写到
		}
		frameLen := int(binary.LittleEndian.Uint32(hdr[8:]))
		if frameLen > maxBodyLen {
			return Message{}, false // 脏数据，停住不误读
		}
		frame := make([]byte, frameLen)
		if _, err := r.f.ReadAt(frame, r.off+12); err != nil {
			return Message{}, false // 半条记录：等写方补全
		}
		seq := binary.LittleEndian.Uint64(hdr)
		r.off += int64(12 + frameLen)
		topic, body, ok := decodeFrame(frame)
		if !ok {
			return Message{}, false
		}
		r.lastSeq = seq
		if r.topic != "" && topic != r.topic {
			continue // 本地过滤：跳过不关心的 topic
		}
		return Message{ID: seq, Topic: topic, Body: body, Created: time.Time{}}, true
	}
}

// LastSeq 最近读到的 seq（诊断/续读游标持久化用）。
func (r *JournalReader) LastSeq() uint64 { return r.lastSeq }

// scanJournal 从头扫一条 size 字节内的记录，返回 (最后一条 seq+1, 干净末尾偏移)。
// 遇到半条/脏记录就在其起始处停下，clean 即最后一个完整记录的末尾。
// want==0 表示「只要末尾」（重放全部时目标是最旧）；want>0 时把 clean 停在第一条 seq>=want 的记录起点。
func scanJournal(f *os.File, size int64, want uint64) (next uint64, clean int64) {
	var off int64
	lastSeq := uint64(0)
	seen := false
	hdr := make([]byte, 12)
	for off+12 <= size {
		if _, e := f.ReadAt(hdr, off); e != nil {
			break
		}
		frameLen := int(binary.LittleEndian.Uint32(hdr[8:]))
		if frameLen < 0 || off+12+int64(frameLen) > size {
			break // 半条
		}
		seq := binary.LittleEndian.Uint64(hdr)
		if want > 0 && seq >= want {
			return seq, off // 停在 want 目标处
		}
		off += 12 + int64(frameLen)
		lastSeq = seq
		seen = true
	}
	if !seen {
		return 0, off
	}
	return lastSeq + 1, off
}
