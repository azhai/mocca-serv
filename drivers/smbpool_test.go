package drivers

import (
	"testing"
	"time"

	"github.com/azhai/mocca/models"
	"github.com/cloudsoda/go-smb2"
)

// fakeDial 返回一条假连接并记录被拨号次数，避免测试依赖真实 SMB 服务。
func fakeDial(counter *int) func() (*smb2.Share, *smb2.Session, error) {
	return func() (*smb2.Share, *smb2.Session, error) {
		*counter++
		return nil, nil, nil // nil 安全：smbCloseConn 会判空
	}
}

func TestSMBAcquireReusesSameConnection(t *testing.T) {
	CloseAllSMB()
	defer CloseAllSMB()

	dials := 0
	dial := fakeDial(&dials)

	c1, err := smbAcquire("storeA", dial)
	if err != nil {
		t.Fatalf("首次获取失败: %v", err)
	}
	c2, err := smbAcquire("storeA", dial)
	if err != nil {
		t.Fatalf("再次获取失败: %v", err)
	}

	if c1 != c2 {
		t.Error("同一个 key 必须复用同一条连接")
	}
	if dials != 1 {
		t.Errorf("只应拨号一次，实际 %d 次", dials)
	}
	if c1.refs != 2 {
		t.Errorf("引用计数应为 2，got %d", c1.refs)
	}

	// 不同 key 必须各自新建，不能串用
	if c3, _ := smbAcquire("storeB", dial); c3 == c1 {
		t.Error("不同 key 不该复用同一条连接")
	}
	if dials != 2 {
		t.Errorf("第二个 key 应再拨号一次，实际 %d 次", dials)
	}
}

func TestSMBReleaseOnlyDropsRefCount(t *testing.T) {
	CloseAllSMB()
	defer CloseAllSMB()

	dials := 0
	dial := fakeDial(&dials)
	c, _ := smbAcquire("storeA", dial)

	smbRelease("storeA")
	if c.refs != 0 {
		t.Errorf("归还后引用应为 0，got %d", c.refs)
	}
	// 归还 ≠ 断开：再次获取仍复用同一条，且不再拨号
	c2, _ := smbAcquire("storeA", dial)
	if c2 != c {
		t.Error("归还后应保留连接供复用")
	}
	if dials != 1 {
		t.Errorf("复用不该再拨号，实际 %d 次", dials)
	}
}

func TestSMBEvictsIdleConnection(t *testing.T) {
	CloseAllSMB()
	defer CloseAllSMB()

	dials := 0
	dial := fakeDial(&dials)
	c, _ := smbAcquire("storeA", dial)
	smbRelease("storeA")

	// 引用为 0 且空闲超过 TTL：应被回收
	c.lastUse = time.Now().Add(-2 * smbIdleTTL)
	if _, err := smbAcquire("storeA", dial); err != nil {
		t.Fatalf("回收后应能重新获取: %v", err)
	}
	if dials != 2 {
		t.Errorf("空闲连接被回收后应重新拨号，实际 %d 次", dials)
	}

	// 仍被引用的连接不能被回收
	c2, _ := smbAcquire("storeB", dial)
	c2.lastUse = time.Now().Add(-2 * smbIdleTTL)
	smbEvictIdleLocked()
	if _, ok := smbConns["storeB"]; !ok {
		t.Error("仍被引用的连接不该被回收")
	}
}

func TestSMBKeyChangesWithConfig(t *testing.T) {
	base := smbAddition{Address: "10.0.0.1", ShareName: "media", Username: "u"}
	s := &models.Storage{ID: 1}

	k1 := smbKey(s, base)
	if k1 != smbKey(s, base) {
		t.Error("相同配置的 key 应稳定")
	}
	other := base
	other.ShareName = "other"
	if k1 == smbKey(s, other) {
		t.Error("共享名变了 key 就该变，否则会串用连接")
	}
	s2 := &models.Storage{ID: 2}
	if k1 == smbKey(s2, base) {
		t.Error("存储不同 key 就该不同")
	}
}
