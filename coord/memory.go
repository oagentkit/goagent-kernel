// memory.go —— Backend 的内存参考实现（**单进程演示保底，不得上生产**）。
//
// # 边界刻在名字与文档里
//
// MemoryBackend 的原子性来自**进程内互斥锁**：三个原语在锁内逐字原子，
// 但那只对同一进程成立。拿它当生产协调存储 = 两个进程各持一把各自的锁，
// 「Acquire 是跨进程原子操作」这条前提不存在——sideeffect 的全部判据
// 会显示为绿，而去重已经失效（AGENTS.md「参考实现是可被误用的资产」
// 那条纪律的原话场景）。
//
// 它在本包的用途只有两个：①给适配器（Board/Store）当**测试保底**——
// 判定逻辑那份实现由契约套件验证时，不需要真实后端；②给宿主当
// **形态示范**——照它的三个方法写 Redis/etcd 版本。
package coord

import (
	"context"
	"sort"
	"sync"
)

// MemoryBackend Backend 的进程内实现（见文件头的边界声明）。
type MemoryBackend struct {
	mu sync.Mutex
	kv map[string]memEntry
}

// memEntry 一个键的当前状态（Val 保存独立拷贝，防调用方改底层数组）。
// del = 墓碑：键已删除但条目保留，版本随之保留（issue #181——删条目即删
// 版本会让同名键重建后版本回到 1，ABA 断言误判「没动过」）。
type memEntry struct {
	ver Version
	val []byte
	del bool
}

// NewMemoryBackend 构造空后端。
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{kv: map[string]memEntry{}}
}

// SingleMachine 仅单机声明（issue #194）：进程内互斥锁就是它的全部原子性，
// 时间真相只有节点本地时钟——显式声明给装配器看，适配器才允许用它判到期
// （并继续给契约套件留 UseClock 注入的口子）。它同时重申本类型的边界：
// 不得上生产。
func (m *MemoryBackend) SingleMachine() bool { return true }

// Snapshot 一致快照读（锁内逐字一致）。
//
// 墓碑键返回 Exists=false 但**携带墓碑版本**（不是 VerMissing）——
// 版本单调的锚点不随删除丢失；VerMissing 只表示「从未写过」。
func (m *MemoryBackend) Snapshot(_ context.Context, keys ...string) (map[string]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Entry, len(keys))
	for _, k := range keys {
		e, ok := m.kv[k]
		switch {
		case !ok:
			out[k] = Entry{Key: k, Ver: VerMissing, Exists: false}
		case e.del:
			out[k] = Entry{Key: k, Ver: e.ver, Exists: false}
		default:
			out[k] = Entry{Key: k, Ver: e.ver, Val: append([]byte(nil), e.val...), Exists: true}
		}
	}
	return out, nil
}

// Txn 条件事务：断言全过才提交（锁内原子），任一失败则一个都不执行。
func (m *MemoryBackend) Txn(_ context.Context, asserts []Assert, mutations []Mutation) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, a := range asserts {
		e, ok := m.kv[a.Key]
		cur := VerMissing
		if ok {
			cur = e.ver
		}
		if cur != a.Ver {
			return false, nil // 正常结论：断言不成立，什么都没发生
		}
	}
	for _, mu := range mutations {
		if mu.Del {
			// 删除也是一次推进：条目转墓碑保留（版本不回退、不复用）。
			// 对从未写过的键 Del 同样立墓碑——它此后与「从未写过」在
			// 版本断言上可辨（VerMissing 断言不再成立）。
			e, ok := m.kv[mu.Key]
			if !ok {
				e = memEntry{ver: VerMissing}
			}
			e.ver++
			e.val = nil
			e.del = true
			m.kv[mu.Key] = e
			continue
		}
		e, ok := m.kv[mu.Key]
		if !ok {
			e = memEntry{ver: VerMissing}
		}
		e.ver++ // 版本单调：每次成功写入 +1
		e.val = append([]byte(nil), mu.Val...)
		e.del = false
		m.kv[mu.Key] = e
	}
	return true, nil
}

// Keys 前缀列键（锁内快照语义，结果按键名排序保证确定性）。
// 墓碑键不列——已删除的键不在键空间里（列出去，适配器的 List 会把
// 已删任务当活任务判）。
func (m *MemoryBackend) Keys(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k, e := range m.kv {
		if e.del {
			continue
		}
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}
