package mediaindex

import (
	"encoding/json"

	"github.com/azhai/mocca/lib/mq"
)

// 弹幕实时推送（进程内发布订阅）。
//
// 为什么用 SSE 而不是 WebSocket：弹幕是**单向**的（服务器 → 客户端），发弹幕走普通的
// POST 就行；SSE 自带断线重连、能穿过反向代理、也不用新增依赖（标准库即可写）。
//
// 扇出复用 lib/mq.Bus：topic 就是「哪块盘 + 哪个视频」的键，每订阅者一根有界 chan，
// 缓冲满了直接丢最新这条（DropNewest）——「丢了不心疼、卡住才是事故」，发送方永不阻塞。
// 相比旧的自建 hub，一条弹幕只 marshal 一次、多个观看者共享同一份字节（旧实现是每个
// 连接各自 enc.Encode 一遍，N 个人看同一视频就重复序列化 N 次）。
//
// 局限（写清楚，别让人踩）：Bus 是**进程内**的广播。多实例部署时，A 进程收到的弹幕
// 推不到连在 B 进程上的客户端 —— 那种规模由 danmaku_bridge.go 用 lib/mq.Journal 补齐
// （配了 DANMAKU_JOURNAL_DIR 才开，默认关）。本项目单进程默认不启用桥，够用。

// danmakuChanBuf 每个订阅者的缓冲条数。
const danmakuChanBuf = 32

var danmakuBus = mq.New(mq.Options{Backlog: danmakuChanBuf, Full: mq.DropNewest})

// danmakuTopic 与读缓存同构的键：存储标识 + sha1。
func danmakuTopic(storageID, sha1hex string) string {
	return storageID + "\x00" + sha1hex
}

// SubscribeDanmaku 订阅某媒体的新弹幕。返回的订阅**必须** Cancel（否则计入上限、内存只涨不降）。
// 收到的是已编码好的弹幕 JSON 字节（Message.Body），SSE 侧直接写出即可，不必再 marshal。
func SubscribeDanmaku(storageID, sha1hex string) (*mq.Subscription, error) {
	return danmakuBus.Subscribe(danmakuTopic(storageID, sha1hex))
}

// PublishDanmaku 把一条新弹幕推给所有正在看这个媒体的人。
// 非阻塞：某个订阅者处理慢（缓冲满）就丢它那一条，不影响其余人，也不拖慢发送这条弹幕的请求。
// 一条消息只编码一次，多订阅者共享同一份字节。
func PublishDanmaku(storageID, sha1hex string, e *Entry) {
	if e == nil {
		return
	}
	topic := danmakuTopic(storageID, sha1hex)
	// 整条链路只 marshal 一次：本地 Bus 扇出与跨进程落盘共用同一份字节（传 []byte 时
	// Bus 与 Journal 都原样取用，不再各自重编码）。
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = danmakuBus.Publish(topic, body)
	// 跨进程桥：没启用（bridge 为 nil）时这一步是 no-op，热路径零成本。
	if b := bridge.Load(); b != nil {
		_ = b.publishOut(topic, body)
	}
}
