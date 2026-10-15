//go:build !unix

package mq

import "errors"

// 非 unix 平台（Windows 等）没有 POSIX 共享内存语义可直接 mmap 一段命名文件区域做
// 无锁环；跨进程请用 Journal（纯文件追加 + 游标读，跨平台）。这里只保留同样的类型与
// 方法签名，让调用方代码跨平台编译，运行时一律返回此错误。
//
// ponytail: 天花板 = Windows 上无共享内存快路径。要真实现，可用 Windows 的
// CreateFileMapping + MapViewOfFile 另写一个 provider，接口不变。
var errShmUnsupported = errors.New("mq: 本平台无共享内存队列，请改用 Journal")

// ShmOptions 见 unix 实现；此处仅为编译兼容。
type ShmOptions struct {
	SlotCount int
	SlotSize  int
}

// Shm 在非 unix 平台不可用。
type Shm struct{}

// ShmReader 在非 unix 平台不可用。
type ShmReader struct{}

// ErrShmClosed 与 unix 侧同名，供跨平台引用。
var ErrShmClosed = errShmUnsupported

// ErrShmTooLarge 与 unix 侧同名，供跨平台引用。
var ErrShmTooLarge = errShmUnsupported

// OpenShm 总是失败。
func OpenShm(path string, opts ShmOptions) (*Shm, error) { return nil, errShmUnsupported }

// Publish 总是失败。
func (s *Shm) Publish(topic string, v any) error { return errShmUnsupported }

// Subscribe 返回 nil 读手；调用方应在 OpenShm 成功后才走到这里。
func (s *Shm) Subscribe(topic string, fromOldest bool) *ShmReader { return nil }

// Close 空操作。
func (s *Shm) Close() error { return nil }

// Next 总是返回 false。
func (r *ShmReader) Next() (Message, bool) { return Message{}, false }

// Dropped 恒为 0。
func (r *ShmReader) Dropped() uint64 { return 0 }
