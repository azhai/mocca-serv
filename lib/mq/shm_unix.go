//go:build unix

package mq

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Shm 是跨进程的共享内存队列：一块 mmap 上的定长槽序号环，写方覆盖式追新，
// 读方各自带游标 seq。慢读者掉队只会跳过自己的消息（记入 Dropped），
// 绝不影响写方，也不影响别的读者。这是「本机高频、丢几条无所谓」场景的快路径；
// 需要「一条不丢 + 能回放」时用 Journal。
//
// 内存布局（全部小端，headerSize 与 slotSize 都是 8 的倍数，保证 seq 字上的原子访问对齐）：
//
//	header: [0:8]magic [8:16]slotSize [16:24]slotCount [24:32]head(下一条要写的 seq)
//	slot i 起始 = headerSize + i*slotSize：
//	        [0:8]写进来的 seq+1（0=从未写过；读方据此判有效） [8:12]frameLen [16:...]frame
//
// 单写者模型：同进程内多个 goroutine 用一把 mu 串行化；跨进程要求一个文件只有一个写方
// （多写方会争 head，这里不做跨进程自旋锁 —— 与现有消费者形状一致，见 ponytail 注释）。
const (
	shmMagic     = uint64(0x4D4F4343_53484D31) // "MOCCSHM1"
	shmHeaderLen = 64
	shmSlotPad   = 16 // frame 从槽内偏移 16 起，留出 seq+len 并对齐
)

// ShmOptions 环的容量。零值可用。
type ShmOptions struct {
	SlotCount int // 槽数，向上取 2 的幂，默认 1024
	SlotSize  int // 每槽字节，向上取 8 的倍数，默认 4096
}

func (o ShmOptions) count() uint64 {
	n := o.SlotCount
	if n <= 0 {
		n = 1024
	}
	// 向上取 2 的幂，用 idx = seq & (count-1) 免取模。
	p := uint64(1)
	for p < uint64(n) {
		p <<= 1
	}
	return p
}

func (o ShmOptions) size() uint64 {
	n := o.SlotSize
	if n <= shmSlotPad {
		n = 4096
	}
	return (uint64(n) + 7) &^ 7 // 向上取 8 的倍数
}

// Shm 一个已打开的共享内存环。
type Shm struct {
	f         *os.File
	data      []byte
	slotSize  uint64
	slotCount uint64
	mask      uint64 // slotCount-1
	mu        sync.Mutex
}

// ErrShmClosed 共享内存已关闭。
var ErrShmClosed = errors.New("mq: 共享内存已关闭")

// ErrShmTooLarge 单条 frame 超过槽可用载荷。
var ErrShmTooLarge = errors.New("mq: 消息超过槽容量")

// OpenShm 打开（或创建）一个共享内存环文件。
func OpenShm(path string, opts ShmOptions) (*Shm, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	created := err == nil
	if os.IsExist(err) {
		f, err = os.OpenFile(path, os.O_RDWR, 0o644)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if created {
		size := int64(shmHeaderLen + opts.count()*opts.size())
		if err := f.Truncate(size); err != nil {
			return nil, err
		}
	}

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	data, err := unix.Mmap(int(f.Fd()), 0, int(fi.Size()), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}

	s := &Shm{data: data}
	if created {
		s.slotSize, s.slotCount = opts.size(), opts.count()
		s.init()
	} else {
		if binary.LittleEndian.Uint64(data) != shmMagic {
			_ = unix.Munmap(data)
			return nil, errors.New("mq: 共享内存文件魔数不符（非本库格式或版本不符）")
		}
		s.slotSize = binary.LittleEndian.Uint64(data[8:])
		s.slotCount = binary.LittleEndian.Uint64(data[16:])
	}
	s.mask = s.slotCount - 1
	return s, nil
}

// init 写头部（仅创建方调用；head 从 0 起）。
func (s *Shm) init() {
	binary.LittleEndian.PutUint64(s.data[0:], shmMagic)
	binary.LittleEndian.PutUint64(s.data[8:], s.slotSize)
	binary.LittleEndian.PutUint64(s.data[16:], s.slotCount)
	atomicStore(s.headAddr(), 0)
}

func (s *Shm) headAddr() *uint64 { return (*uint64)(unsafe.Pointer(&s.data[24])) }

func (s *Shm) slotSeq(idx uint64) *uint64 {
	off := uint64(shmHeaderLen) + idx*s.slotSize
	return (*uint64)(unsafe.Pointer(&s.data[off]))
}

func (s *Shm) slotBytes(idx uint64) []byte {
	off := uint64(shmHeaderLen) + idx*s.slotSize
	return s.data[off : off+s.slotSize]
}

// Publish 往环里写一条消息，覆盖最旧的槽（若已绕回）。v 编码规则同 Bus.Publish。
func (s *Shm) Publish(topic string, v any) error {
	body, err := encodeBody(v)
	if err != nil {
		return err
	}
	frame := encodeFrame(topic, body)
	if uint64(len(frame)) > s.slotSize-shmSlotPad {
		return ErrShmTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := atomicLoad(s.headAddr())
	idx := seq & s.mask
	slot := s.slotBytes(idx)
	atomicStore(s.slotSeq(idx), 0) // 先清有效位，避免读方在半写时读到旧载荷
	binary.LittleEndian.PutUint32(slot[8:], uint32(len(frame)))
	copy(slot[shmSlotPad:], frame)
	atomicStore(s.slotSeq(idx), seq+1) // release：载荷就绪后才翻有效
	atomicStore(s.headAddr(), seq+1)
	return nil
}

// Subscribe 新建一个游标。from 为 true 时从环里现存最旧的一条开始读，
// 否则从「订阅之后新写的」开始（默认追新）。topic 传空串读全部。
func (s *Shm) Subscribe(topic string, fromOldest bool) *ShmReader {
	head := atomicLoad(s.headAddr())
	var cur uint64
	if fromOldest {
		oldest := int64(head) - int64(s.slotCount)
		if oldest < 0 {
			oldest = 0
		}
		cur = uint64(oldest)
	} else {
		cur = head
	}
	return &ShmReader{s: s, topic: topic, cur: cur}
}

// Close 解除映射。
func (s *Shm) Close() error {
	if s.data == nil {
		return nil
	}
	err := unix.Munmap(s.data)
	s.data = nil
	return err
}

// ShmReader 共享内存环的一路读者，自带游标。
type ShmReader struct {
	s       *Shm
	topic   string
	cur     uint64
	dropped uint64
}

// Next 取下一条（按 topic 过滤后）消息。追上写方时返回 ok=false（调用方 poll）。
// 掉队（环绕回覆盖了没读的槽）时跳过并计入 Dropped。
func (r *ShmReader) Next() (Message, bool) {
	for attempts := 0; attempts < 8; attempts++ {
		head := atomicLoad(r.s.headAddr())
		if r.cur >= head {
			return Message{}, false // 追上写方了
		}
		oldest := int64(head) - int64(r.s.slotCount)
		if oldest < 0 {
			oldest = 0
		}
		if int64(r.cur) < oldest { // 太慢，最旧的已被覆盖：跳到现存最旧
			r.dropped += uint64(oldest) - r.cur
			r.cur = uint64(oldest)
			continue
		}
		idx := r.cur & r.s.mask
		if atomicLoad(r.s.slotSeq(idx)) != r.cur+1 { // 正被写方覆盖/半写：重来
			continue
		}
		slot := r.s.slotBytes(idx)
		frameLen := int(binary.LittleEndian.Uint32(slot[8:]))
		frame := slot[shmSlotPad : shmSlotPad+uint64(frameLen)]
		topic, body, ok := decodeFrame(frame)
		seq := r.cur
		r.cur++
		if !ok {
			continue
		}
		if r.topic != "" && topic != r.topic {
			continue // 本地过滤
		}
		return Message{ID: seq, Topic: topic, Body: append([]byte(nil), body...), Created: time.Time{}}, true
	}
	return Message{}, false
}

// Dropped 因掉队被跳过的条数。
func (r *ShmReader) Dropped() uint64 { return r.dropped }

// 原子访问集中在这里：mmap 区域用 unsafe.Pointer 取到的 *uint64 做原子读写。
func atomicLoad(p *uint64) uint64     { return atomic.LoadUint64(p) }
func atomicStore(p *uint64, v uint64) { atomic.StoreUint64(p, v) }
