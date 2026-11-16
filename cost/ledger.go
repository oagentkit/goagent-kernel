// ledger.go —— Ledger：进程内参考记账本（口径怎么落的演示体）。
package cost

import (
	"iter"
	"slices"
	"sync"
)

// Ledger 成本账本（进程内参考实现）。零值可用，并发安全（编排可能从多个
// 环节同时入账）。**不是持久化承诺**：跨进程聚合与落库归宿主（见包注释
// 「内核给口径，不给存储」）。
type Ledger struct {
	mu     sync.Mutex
	events []Event
}

// Record 入账一条事件。
//
// 口径不合格（空 Kind / 负量）拒收并返回哨兵——账本的失败形态是变红，
// 不是把坏账悄悄吞掉。入账时**整份拷贝**事件（含 Dims）：调用方复用 map
// 改写不影响已入账的账面。
func (l *Ledger) Record(e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	cp := e.Clone()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, cp)
	return nil
}

// Events 快照迭代（range-over-func）：流式喂给 Aggregate 或宿主自己的
// 消费面，不必先物化成切片再传。
//
// 迭代的是**出账时再拷贝**的副本：Record 侧的拷贝挡的是「入账后被调用方
// 改写」，这里的拷贝挡的是「聚合侧改写账面」——两个方向都堵死，账面才是
// 只读的。快照在锁内物化，迭代在锁外进行：聚合慢不阻塞继续入账。
//
// 锁内只做 Event 结构体的浅拷贝（切片复制），Dims 的深拷贝留到锁外逐条做：
// 账本内部的 Dims 在 Record 后从不改写，浅拷贝不会让聚合侧看到中间态；
// 这样锁持有时间只与事件**条数**成正比，不与每条 Dims 的规模叠加。
//
// 提前终止（break）安全：停止拉取即可，账面不受影响。
func (l *Ledger) Events() iter.Seq[Event] {
	return func(yield func(Event) bool) {
		l.mu.Lock()
		snap := slices.Clone(l.events)
		l.mu.Unlock()
		for _, e := range snap {
			if !yield(e.Clone()) {
				return
			}
		}
	}
}

// Reset 清空账本（释放全部已入账事件）。
//
// # 为什么需要显式生命周期边界
//
// Ledger 被文档定位为「每轮计量」的进程内参考实现，但事件数没有上界：
// 长生命周期进程里只 append 会让账本单调增长，Events() 的出账成本也随之
// 线性增长。清理触发点交给宿主（按会话重建 / 每轮 Reset），内核提供入口
// 并保证清理后 Count 归零、Events 不再产出旧事件。
//
// 并发安全：与 Record/Events 共用同一把锁；Reset 与一次在途的 Events
// 快照互不阻塞（快照已物化，读到的是 Reset 前的那个时点）。
func (l *Ledger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = nil
}

// Count 已入账事件数（账本规模的可观测面）。
func (l *Ledger) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}
