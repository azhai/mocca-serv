package mediaindex

import "sync"

// 弹幕实时推送（进程内发布订阅）。
//
// 为什么用 SSE 而不是 WebSocket：弹幕是**单向**的（服务器 → 客户端），发弹幕走普通的
// POST 就行；SSE 自带断线重连、能穿过反向代理、也不用新增依赖（标准库即可写）。
//
// 局限（写清楚，别让人踩）：这是**进程内**的广播。多实例部署时，A 进程收到的弹幕
// 推不到连在 B 进程上的客户端 —— 那种规模要换成 Redis Pub/Sub 之类的外部通道。
// 本项目是单进程，够用。

// danmakuChanBuf 每个订阅者的缓冲。弹幕是"丢了不心疼、卡住才是事故"的数据：
// 缓冲满了直接丢弃最新这条，绝不让发送方阻塞在广播上。
const danmakuChanBuf = 32

type danmakuHubT struct {
	mu   sync.RWMutex
	subs map[string]map[chan *Entry]struct{} // key 与读缓存同构：存储标识 + sha1
}

var danmakuHub = &danmakuHubT{subs: map[string]map[chan *Entry]struct{}{}}

func (h *danmakuHubT) key(storageID, sha1hex string) string {
	return storageID + "\x00" + sha1hex
}

// SubscribeDanmaku 订阅某媒体的新弹幕。
// 返回只读 channel 与**必须**调用的取消函数（否则订阅会一直留着，内存只涨不降）。
func SubscribeDanmaku(storageID, sha1hex string) (<-chan *Entry, func()) {
	ch := make(chan *Entry, danmakuChanBuf)
	key := danmakuHub.key(storageID, sha1hex)

	danmakuHub.mu.Lock()
	set, ok := danmakuHub.subs[key]
	if !ok {
		set = map[chan *Entry]struct{}{}
		danmakuHub.subs[key] = set
	}
	set[ch] = struct{}{}
	danmakuHub.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			danmakuHub.mu.Lock()
			if set, ok := danmakuHub.subs[key]; ok {
				delete(set, ch)
				if len(set) == 0 {
					delete(danmakuHub.subs, key) // 没人订阅了就别留空壳
				}
			}
			danmakuHub.mu.Unlock()
		})
	}
	return ch, cancel
}

// PublishDanmaku 把一条新弹幕推给所有正在看这个媒体的人。
// 非阻塞：某个订阅者处理慢（缓冲满）就跳过它，不影响其余人，也不拖慢发送这条弹幕的请求。
func PublishDanmaku(storageID, sha1hex string, e *Entry) {
	if e == nil {
		return
	}
	key := danmakuHub.key(storageID, sha1hex)
	danmakuHub.mu.RLock()
	chs := make([]chan *Entry, 0, len(danmakuHub.subs[key]))
	for ch := range danmakuHub.subs[key] {
		chs = append(chs, ch)
	}
	danmakuHub.mu.RUnlock()

	for _, ch := range chs {
		select {
		case ch <- e:
		default: // 缓冲满：丢这一条，不阻塞
		}
	}
}
