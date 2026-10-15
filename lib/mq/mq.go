// Package mq 提供两种队列，按需选一种：
//
//  1. Bus —— 进程内 topic 扇出（pub/sub）。每订阅者一根有界 chan，
//     发布方永不因为某个慢读者被拖住（满了按策略丢，并计数可查）。
//     这是 mediaindex 弹幕 hub 的形状：连接数不可控，事件源不能被卡。
//  2. Shm / Journal —— 跨进程队列。Shm 是 mmap 上的定长槽序号环（unix），
//     Journal 是纯文件追写日志（跨平台，含 Windows，见 journal.go）。
//     两者都是「读者各自带游标」的模型：慢读者掉队只会丢自己的消息，
//     不影响发布方，也不影响别的读者。
//
// 刻意不做的事（都有代价，写在这里而不是藏在代码里）：
//   - 不做 ack / 重发 / 持久化确认（gobus 有，靠 fastcache + cbor 两个依赖撑着）。
//     本仓库唯一的真实消费者是 SSE 扇出与索引增量通知：丢一条的后果是
//     「画面少一条弹幕」，不是「数据不一致」，加 ack 只会把复杂度搬到每一层。
//   - 不做 topic 通配匹配。跨进程侧的 topic 过滤在读者本地做，量级用不上路由表。
//   - 不做优先级（gobus 的 1~5 级需要每 topic 一条有序待分队列 + 唤醒线程）。
package mq

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Message 一条消息。Topic 是路由键，Body 是原样字节。
//
// 发布方给结构体、给字符串、给已编码好的 JSON 都行：Bus 只保证「编码一次，
// 多个订阅者拿到同一份 bytes」，不做每订阅者重复 marshal。
type Message struct {
	ID      uint64    `json:"id"`
	Topic   string    `json:"topic"`
	Body    []byte    `json:"body"`
	Created time.Time `json:"created"`
}

// FullPolicy 订阅者队列满时的处置。
type FullPolicy int

const (
	// DropNewest 丢掉正在发的这条（默认）：读者的历史顺序不受扰动。
	DropNewest FullPolicy = iota
	// DropOldest 丢掉读者队里最旧的一条，腾位置给新的：适合「只关心最新状态」的读者。
	DropOldest
	// Block 阻塞发布方直到有位：只有「一条都不能丢」且读者数量可控时才用，
	// 一个卡住的读者会拖住整个发布方。
	Block
)

// Options Bus 的容量与策略。零值可用。
type Options struct {
	Backlog int        // 每订阅者的缓冲条数，默认 16
	MaxSubs int        // 同时在场的订阅者上限，默认 4096
	Full    FullPolicy // 队列满时的处置，默认 DropNewest
}

func (o Options) backlog() int {
	if o.Backlog > 0 {
		return o.Backlog
	}
	return 16
}

func (o Options) maxSubs() int {
	if o.MaxSubs > 0 {
		return o.MaxSubs
	}
	return 4096
}

// ErrClosed 总线已 Close。
var ErrClosed = errors.New("mq: 总线已关闭")

// ErrTooManySubs 订阅者已达上限（挡住连接泄漏，与 SSE 的连接数上限同思路）。
var ErrTooManySubs = errors.New("mq: 订阅者已达上限")

// sub 一个订阅者：一根数据 chan + 一根 done 信号 chan + 自己的计数。
type sub struct {
	topic string
	ch    chan Message
	done  chan struct{} // Cancel/Close 只关这根，不关 ch
	once  sync.Once
	drop  atomic.Uint64
	got   atomic.Uint64
}

// quit 幂等地发出结束信号。数据 chan 全程不关：
// 发布方可能在 Cancel 之后还握着一份订阅快照，往已关的 chan 上 send 会直接 panic；
// 把「结束」与「数据」分成两根通道，这个竞争窗口就不存在了，代价是读者得用 Next/select 而不是 range。
func (s *sub) quit() { s.once.Do(func() { close(s.done) }) }

// Subscription 一次订阅。用完必须 Cancel（幂等）。
type Subscription struct {
	s *sub
	b *Bus
}

// C 数据通道。不要 range 它 —— 它不会被关闭，结束信号在 Done 上；
// 要么用 Next，要么自己 select 两根。
func (s *Subscription) C() <-chan Message { return s.s.ch }

// Done 订阅被取消（或总线 Close）时关闭。
func (s *Subscription) Done() <-chan struct{} { return s.s.done }

// Next 阻塞取一条：拿到 (msg, true)，订阅结束时拿到 (零, false)。
// SSE 写循环用它最省事：`for m, ok := sub.Next(); ok; m, ok = sub.Next() { … }`。
func (s *Subscription) Next() (Message, bool) {
	select {
	case m := <-s.s.ch:
		return m, true
	case <-s.s.done:
		// done 关了但队列里可还有货：先把存量读完再收尾，不抖掉已投递的弹幕。
		select {
		case m := <-s.s.ch:
			return m, true
		default:
			return Message{}, false
		}
	}
}

// Topic 订阅的主题。
func (s *Subscription) Topic() string { return s.s.topic }

// Dropped 因为队列满而被丢弃的条数（诊断用：非零说明这个读者跟不上）。
func (s *Subscription) Dropped() uint64 { return s.s.drop.Load() }

// Received 已投递（尚未被读走也可能计入）的条数。
func (s *Subscription) Received() uint64 { return s.s.got.Load() }

// Cancel 摘掉这个订阅并发出结束信号。重复调用安全。
func (s *Subscription) Cancel() {
	b := s.b
	b.mu.Lock()
	if _, ok := b.subs[s.s]; ok {
		delete(b.subs, s.s)
		b.n--
	}
	b.mu.Unlock()
	s.s.quit() // 不在锁里：quit 自己幂等，锁外调也行
}

// Bus 进程内事件总线。
type Bus struct {
	mu   sync.RWMutex
	subs map[*sub]struct{}
	// n 在场订阅者数：只在 mu 里读写（计数不值得再来一重原子量）。
	n int

	opts Options
	seq  atomic.Uint64 // 消息序号（只在内存总线里递增，与跨进程侧的 ID 不同源）
	open atomic.Bool
}

// New 一条总线。opts 传零值即默认。
func New(opts Options) *Bus {
	b := &Bus{opts: opts, subs: map[*sub]struct{}{}}
	b.open.Store(true)
	return b
}

// Subscribers 当前订阅者数（测试与诊断用）。
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.n
}

// Publish 把 v 发给订阅了 topic 的所有读者，返回收到的订阅者数。
//
// v 的编码规则：nil → 空 body；[]byte / string → 原样字节（已经是 JSON 的不要再包一层）；
// 其余走 encoding/json，一次编码、多读者共享。
// 没有任何订阅者时直接返回 0，不做编码、不留历史。
func (b *Bus) Publish(topic string, v any) (int, error) {
	if !b.open.Load() {
		return 0, ErrClosed
	}
	subs := b.match(topic)
	if len(subs) == 0 {
		return 0, nil
	}
	body, err := encodeBody(v)
	if err != nil {
		return 0, err
	}
	m := Message{
		ID:      b.seq.Add(1),
		Topic:   topic,
		Body:    body,
		Created: time.Now(),
	}
	for _, s := range subs {
		deliver(s, m, b.opts.Full)
	}
	return len(subs), nil
}

// PublishJSON 直接发一段现成的 JSON（省掉一次 marshal）。
func (b *Bus) PublishJSON(topic string, raw json.RawMessage) (int, error) {
	return b.Publish(topic, []byte(raw))
}

// match 取当前订阅了该 topic 的订阅者快照：
// 发布路径不在锁里做投递，避免慢读者把写锁攥住。
func (b *Bus) match(topic string) []*sub {
	b.mu.RLock()
	out := make([]*sub, 0, len(b.subs))
	for s := range b.subs {
		if s.topic == topic {
			out = append(out, s)
		}
	}
	b.mu.RUnlock()
	return out
}

// deliver 按策略投递一条消息。三种策略都不会因读者消失而 panic：
// 数据 chan 不关，发送只多一个 done 分支。
func deliver(s *sub, m Message, policy FullPolicy) {
	s.got.Add(1)
	switch policy {
	case Block:
		select {
		case s.ch <- m:
		case <-s.done:
		}
	case DropOldest:
		for {
			select {
			case s.ch <- m:
				return
			case <-s.done:
				return
			default:
				select { // 丢最旧的一条再试
				case <-s.ch:
					s.drop.Add(1)
				default:
				}
			}
		}
	default: // DropNewest
		select {
		case s.ch <- m:
		default:
			s.drop.Add(1)
		}
	}
}

// encodeBody 按 Publish 的约定把 v 变字节。
func encodeBody(v any) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []byte:
		return t, nil
	case string:
		return []byte(t), nil
	case json.RawMessage:
		return t, nil
	default:
		return json.Marshal(t)
	}
}

// Subscribe 订阅一个主题。拿到的 Subscription 用完务必 Cancel，否则计入上限。
func (b *Bus) Subscribe(topic string) (*Subscription, error) {
	if !b.open.Load() {
		return nil, ErrClosed
	}
	b.mu.Lock()
	if b.n >= b.opts.maxSubs() { // 计数与表在同一把锁里，不存能读到脏值的路径
		b.mu.Unlock()
		return nil, ErrTooManySubs
	}
	s := &sub{topic: topic, ch: make(chan Message, b.opts.backlog()), done: make(chan struct{})}
	b.subs[s] = struct{}{}
	b.n++
	b.mu.Unlock()
	return &Subscription{s: s, b: b}, nil
}

// Close 关掉所有订阅（只发结束信号，不动数据 chan）。之后 Publish/Subscribe 返回 ErrClosed。
func (b *Bus) Close() error {
	if !b.open.Swap(false) {
		return nil // 幂等
	}
	b.mu.Lock()
	victims := make([]*sub, 0, len(b.subs))
	for s := range b.subs {
		victims = append(victims, s)
		delete(b.subs, s)
	}
	b.n = 0
	b.mu.Unlock()
	for _, s := range victims {
		s.quit()
	}
	return nil
}
