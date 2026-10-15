package mq

import (
	"encoding/binary"
)

// 跨进程侧的记录形状（两条通道共用同一套编解码）：
//
//	frame = [2B topicLen][topic 字节][body 字节]
//
// Journal 在外面再套 [4B frameLen][8B seq]，Shm 把长度放进定长槽的槽头。
// 字节序统一小端：共享内存只在同一台机器上的进程之间用，不做跨机传输。
const maxTopicLen = 1<<16 - 1
const maxBodyLen = 1<<31 - 1

// encodeFrame 把 topic 与 body 编成一条 frame。
func encodeFrame(topic string, body []byte) []byte {
	f := make([]byte, 2+len(topic)+len(body))
	binary.LittleEndian.PutUint16(f, uint16(len(topic)))
	copy(f[2:], topic)
	copy(f[2+len(topic):], body)
	return f
}

// decodeFrame 解出 topic 与 body。数据不足或长度对不上时返回 false
// （跨进程读到半条记录是正常现象，调用方按「还没写好」处理）。
func decodeFrame(f []byte) (topic string, body []byte, ok bool) {
	if len(f) < 2 {
		return "", nil, false
	}
	tn := int(binary.LittleEndian.Uint16(f))
	if tn > len(f)-2 {
		return "", nil, false
	}
	return string(f[2 : 2+tn]), f[2+tn:], true
}

// encodeBodyOf 按 Publish 的约定取字节（与 Bus 同一套规则，行为一致好记）。
func encodeBodyOf(v any) ([]byte, error) { return encodeBody(v) }
