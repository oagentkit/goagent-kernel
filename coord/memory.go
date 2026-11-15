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
//
// tomb 是墓碑标记：删除不丢条目，版本保留（Exists=false 由 tomb 表达）。
// 墓碑**本实现不 GC**——删掉墓碑等于把该键的版本还给 VerMissing，同名键
// 重建后版本从 1 重来，旧代快照的断言重新成立（ABA）。宿主的 GC 许可条件
// 见 Backend 接口文档的 Version 条款（最稳妥的形态是不 GC）。
type memEntry struct {
	ver  Version
	val  []byte
	tomb bool
}

// NewMemoryBackend 构造空后端。
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{kv: map[string]memEntry{}}
}

// Snapshot 一致快照读（锁内逐字一致）。
func (m *MemoryBackend) Snapshot(_ context.Context, keys ...string) (map[string]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Entry, len(keys))
	for _, k := range keys {
		e, ok := m.kv[k]
		if !ok {
			out[k] = Entry{Key: k, Ver: VerMissing, Exists: false}
			continue
		}
		if e.tomb {
			// 墓碑：键不存在，但版本保留（删除也是一次成功写入）。
			out[k] = Entry{Key: k, Ver: e.ver, Exists: false}
			continue
		}
		out[k] = Entry{Key: k, Ver: e.ver, Val: append([]byte(nil), e.val...), Exists: true}
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
		e, ok := m.kv[mu.Key]
		if mu.Del {
			// 删除也是一次成功写入：活条目转墓碑并推进版本（版本跨删除存活，
			// 同名键重建后从墓碑之后继续——ABA 的封死点）。墓碑上没有代可灭，
			// 从未存在的键也无代可灭：两者都是空操作（不消耗版本）。
			if !ok || e.tomb {
				continue
			}
			e.ver++
			e.val = nil
			e.tomb = true
			m.kv[mu.Key] = e
			continue
		}
		if !ok {
			e = memEntry{ver: VerMissing}
		}
		e.ver++ // 版本单调：每次成功写入 +1
		e.val = append([]byte(nil), mu.Val...)
		e.tomb = false
		m.kv[mu.Key] = e
	}
	return true, nil
}

// Keys 前缀列键（锁内快照语义，结果按键名排序保证确定性）。
func (m *MemoryBackend) Keys(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k, e := range m.kv {
		if e.tomb {
			continue // 墓碑不是「存在的键」：前缀列键只列活键
		}
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}
