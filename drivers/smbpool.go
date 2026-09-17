package drivers

import (
	"fmt"
	"sync"
	"time"

	"github.com/azhai/mocca/models"
	"github.com/cloudsoda/go-smb2"
)

// SMB 拨号 + 挂载一次要几百毫秒，而每个 HTTP 请求都会 Open 一次驱动。
// 若每次都重连，列个目录就要重新握手，既慢又会在服务端堆积会话，
// 所以这里做一层连接复用：按「存储 + 地址 + 共享 + 账号」缓存，
// Close() 只是归还引用而不是断开，空闲超时才真正关闭。
const smbIdleTTL = 5 * time.Minute

// smbConn 一条被复用的 SMB 连接。
type smbConn struct {
	share   *smb2.Share
	session *smb2.Session
	refs    int
	lastUse time.Time
}

var (
	smbMu    sync.Mutex
	smbConns = map[string]*smbConn{}
)

// smbKey 连接缓存键：存储或连接参数一变就是新键，不会串用旧连接。
func smbKey(s *models.Storage, a smbAddition) string {
	return fmt.Sprintf("%d|%s|%s|%s", s.ID, a.Address, a.ShareName, a.Username)
}

// smbAcquire 取一条连接：命中缓存则复用，否则用 dial 新建。
// dial 作为参数传入，便于测试注入假实现。
func smbAcquire(key string, dial func() (*smb2.Share, *smb2.Session, error)) (*smbConn, error) {
	smbMu.Lock()
	defer smbMu.Unlock()

	smbEvictIdleLocked()
	if c, ok := smbConns[key]; ok {
		c.refs++
		c.lastUse = time.Now()
		return c, nil
	}

	share, session, err := dial()
	if err != nil {
		return nil, err
	}
	c := &smbConn{share: share, session: session, refs: 1, lastUse: time.Now()}
	smbConns[key] = c
	return c, nil
}

// smbRelease 归还连接：只减引用，不立刻断开，留给下次复用。
func smbRelease(key string) {
	smbMu.Lock()
	defer smbMu.Unlock()
	if c, ok := smbConns[key]; ok {
		if c.refs > 0 {
			c.refs--
		}
		c.lastUse = time.Now()
	}
}

// smbEvictIdleLocked 关闭空闲超时的连接。调用方需持有 smbMu。
// 顺带清理断连后遗留的引用计数为 0 的条目。
func smbEvictIdleLocked() {
	for k, c := range smbConns {
		if c.refs > 0 || time.Since(c.lastUse) <= smbIdleTTL {
			continue
		}
		smbCloseConn(c)
		delete(smbConns, k)
	}
}

// smbCloseConn 真正断开：先卸载共享再注销会话。
// share/session 可能为 nil（测试注入的假连接），所以都要判空。
func smbCloseConn(c *smbConn) {
	if c.share != nil {
		_ = c.share.Umount()
	}
	if c.session != nil {
		_ = c.session.Logoff()
	}
}

// CloseAllSMB 关闭全部缓存连接，供进程退出或测试使用。
func CloseAllSMB() {
	smbMu.Lock()
	defer smbMu.Unlock()
	for k, c := range smbConns {
		smbCloseConn(c)
		delete(smbConns, k)
	}
}
