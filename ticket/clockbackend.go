// clockbackend.go —— 存储时钟权威的票据存储接缝（issue #186）。
//
// # 为什么 Backend 之外还要有第二个接缝
//
// Backend 的 Take/GC 把调用方的 now 当过期判据：单副本下「进程时钟 = 唯一时钟」
// 语义正确，多副本下**每个副本的时钟偏移都在改写「票据是否过期」**——
//   - 时钟快的副本把未过期票据判成过期，而 Take 是先删后判，这次误判**同时
//     删除**了票据 ⇒ 真正持有者随后消费失败且不可恢复；
//   - 时钟慢的副本让一次性授权过期后仍可重放（防重放语义被打开缺口）。
//
// 直接给 Backend.Take/GC 去掉 now 是**改已有导出符号的签名**——本包 Stable
// （v0.33.0 定级），breaking 不允许，故按 docs/api-compat.md 的 additive
// 路径另立本接缝（issue 的「接口去掉 now」形态）。
//
// # 存储时钟权威（时间的真相只有存储一个）
//
// 本接缝的三条硬契约：
//
//  1. **过期判定只用存储时钟**：Take/GC 的过期比较由实现内部完成，节点时钟
//     不得参与——DB 形态用 `expires_at < now()`，Redis 形态用键 TTL 自带
//     的过期。签发侧给的 TTL 是**时长**不是时刻：实现以**自己的**当前时刻
//     加 TTL 定 expires_at（与 taskboard.ClaimRequest 同一条理由：调用方
//     时钟与存储时钟可能对不上，给时长、存储自己算，两边不会各算一套）。
//     - **先删后判顺序不变**：过期票据同样被 Take 删除（判定语义与 Backend
//     逐字一致——先删后判是语义的一部分，不是实现巧合）。
//     - **原子性契约不变**：Take 必须原子（多副本并发 Take 同一 id 恰好一个
//     ok=true），见 backend.go 文件头。
//
// # 进程内参考实现
//
// NewMemoryBackendWithClock：单进程成立，**不得上生产**（跨进程原子性它
// 给不了）。时钟可注入 ⇒ 「过期判定只随存储时钟」可以被确定性测。
package ticket

import (
	"fmt"
	"sync"
	"time"
)

// StorageClockBackend 存储时钟权威的票据存储接缝（issue #186）。
//
// 与 [Backend] 是**同一契约的两种形态**：Backend 是 Stable 兼容保留的
// 调用方时钟形态，本接缝是「接口去掉 now」的目标形态——时间的真相只有
// 存储一个。方法语义与 Backend 一一对应，差别只有两处：过期判定不收调用方
// now；Put 收**时长**并返回存储落下的权威过期时刻。跨进程实现请实现本接缝
// 并经 [NewWithStorageClock] 注入（原子性契约见 backend.go 文件头）。
type StorageClockBackend interface {
	// Put 写入一张未消费票据：实现以**自己的当前时刻 + ttl** 定 expires_at
	// （节点时钟不参与），返回落下的权威过期时刻。容量策略由实现自判
	// （内存参考实现满了报错，与旧实现「票据空间已满」同一拒发口径）。
	Put(id string, binding Binding, ttl time.Duration) (expiresAt time.Time, err error)
	// Take 原子取出并删除：不存在或已过期 ⇒ ok=false（过期判定用存储时钟）。
	//
	// ⚠️ 过期票据**也已被删除**——先删后判的顺序是语义的一部分（见文件头）。
	Take(id string) (Binding, bool, error)
	// GC 删除已过期票据（存储时钟判定），返回清理张数。
	GC() (int, error)
	// Len 返回现存票据数（含过期未清理的——它们只是不可消费，仍占空间）。
	Len() (int, error)
}

// memoryClockBackend StorageClockBackend 的进程内参考实现。
//
// 与 memoryBackend 同构（满容量拒发、先删后判），差别是时钟住在**存储里**：
// 过期判定只用 b.now，签发侧给什么都改写不了「票据是否过期」。
type memoryClockBackend struct {
	mu      sync.Mutex
	m       map[string]entry
	maxSize int
	// now 存储自己的时钟（时间的真相只有这一个；nil 由构造函数落 time.Now）。
	now func() time.Time
}

// NewMemoryBackendWithClock 构造进程内参考实现（存储时钟权威形态，
// issue #186 验收的「memoryBackend 用注入时钟实现同一语义」）。
// now 为 nil ⇒ time.Now。maxSize <= 0 ⇒ DefaultMaxEntries。
func NewMemoryBackendWithClock(maxSize int, now func() time.Time) StorageClockBackend {
	if maxSize <= 0 {
		maxSize = DefaultMaxEntries
	}
	if now == nil {
		now = time.Now
	}
	return &memoryClockBackend{m: map[string]entry{}, maxSize: maxSize, now: now}
}

// Put 实现 StorageClockBackend：以**存储时钟**落过期时刻（签发侧给的
// 只是 TTL 时长）；容量已满 ⇒ 显式报错。
func (b *memoryClockBackend) Put(id string, binding Binding, ttl time.Duration) (time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.m) >= b.maxSize {
		return time.Time{}, fmt.Errorf("票据空间已满（上限 %d 张）", b.maxSize)
	}
	exp := b.now().Add(ttl)
	b.m[id] = entry{binding: binding, expiresAt: exp}
	return exp, nil
}

// Take 实现 StorageClockBackend（单锁内取/删/判过期，先删后判顺序与旧实现一致）。
func (b *memoryClockBackend) Take(id string) (Binding, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tk, ok := b.m[id]
	if !ok {
		return Binding{}, false, nil
	}
	delete(b.m, id)
	if b.now().After(tk.expiresAt) {
		return Binding{}, false, nil
	}
	return tk.binding, true, nil
}

// GC 实现 StorageClockBackend（存储时钟判定）。
func (b *memoryClockBackend) GC() (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for id, tk := range b.m {
		if b.now().After(tk.expiresAt) {
			delete(b.m, id)
			n++
		}
	}
	return n, nil
}

// Len 实现 StorageClockBackend（含过期未清理的）。
func (b *memoryClockBackend) Len() (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.m), nil
}
