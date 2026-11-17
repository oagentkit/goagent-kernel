// quota_clock.go —— 存储时钟的调用纪律（issue #366）。
//
// 时钟是**宿主注入的回调**，不是内核自己的时间源：它回答「现在几点」，而这个
// 答案决定预占是否到期、额度能不能被回收。
//
// # 纪律：时钟回调一律在**存储锁外**调用
//
// sync.Mutex 不可重入，而宿主在时钟回调里回查本包（读一次用量是极自然的
// 写法）会二次抢同一把锁 ⇒ **永久死锁**。失效形态是**静默挂起**：调用方拿
// 不到任何错误，只看到这次调用永远不返回——比报错更难查。
//
// 仓内已有同款范式：coord/store.go 的 nowAt（锁内取函数值、解锁后再调用）
// 与 ticket/backend.go 的 Put（exp 在加锁之前算）。
package scheduling

import "time"

// now 存储侧当前时刻。
//
// 判据是**先在锁内取值、解锁后再调用**（与 coord/store.go 的 nowAt 同一
// 范式）：宿主注入的时钟回调若在锁内被调用，「回调里回查本包」就是永久
// 死锁。
//
// 代价：now() 自己要抢 m.mu ⇒ **不得在持有 m.mu 时调用**（那是自死锁）。
// 三条调用路径（ReserveLease / Reclaim / SweepExpired）都在锁外取值。
func (m *MemQuotaStore) now() time.Time {
	m.mu.Lock()
	clock := m.clock
	m.mu.Unlock()
	return clock()
}

// untilAfter 有效期终点（ttl <= 0 = 不限期，零值）。
//
// 时钟同样在**锁外**取值——与 now() 同一纪律。
func (m *MemQuotaStore) untilAfter(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return m.now().Add(ttl)
}
