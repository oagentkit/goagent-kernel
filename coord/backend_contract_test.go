package coord

import (
	"context"
	"testing"
)

// 测试夹具全部合成数据（AGENTS.md 红线）。

// TestMemoryBackendContract 内存参考实现接入 Backend 契约套件
// （硬失败不跳过——接入即声明满足契约）。
func TestMemoryBackendContract(t *testing.T) {
	ctx := context.Background()
	if err := BackendContract(ctx, NewMemoryBackend(), "contract/"); err != nil {
		t.Fatalf("MemoryBackend 必须过 Backend 契约套件: %v", err)
	}
}

// TestBackendContractRejectsBadInput 套件入口不静默：nil 后端与空前缀都是
// 「判据无法给出结论」，必须当场报错（否则会跑出一组空断言）。
func TestBackendContractRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	if err := BackendContract(ctx, nil, "ns/"); err == nil {
		t.Fatal("nil 后端必须被套件拒绝")
	}
	if err := BackendContract(ctx, NewMemoryBackend(), ""); err == nil {
		t.Fatal("空键空间前缀必须被套件拒绝")
	}
}

// TestBackendContractRedOnVersionResetBackend 反向验证（AGENTS.md 纪律：
// 判据必须有「坏实现会红」的用例）：复刻修复前的删除语义——Del 直接丢条目、
// 版本随键生灭。套件必须在「删后重建旧版本 CAS」上判红。
func TestBackendContractRedOnVersionResetBackend(t *testing.T) {
	err := BackendContract(context.Background(), &versionResetBackend{inner: NewMemoryBackend()}, "contract/")
	if err == nil {
		t.Fatal("删除即丢版本的坏实现必须被契约套件判红——判据没牙齿等于没有判据")
	}
}

// TestBackendContractRedOnAssertIgnoringBackend 反向验证之二：断言与写入
// 分离的实现（brokenBackend 忽略全部断言）必须被套件判红。
func TestBackendContractRedOnAssertIgnoringBackend(t *testing.T) {
	err := BackendContract(context.Background(), &brokenBackend{inner: NewMemoryBackend()}, "contract/")
	if err == nil {
		t.Fatal("断言与写入未原子的坏实现必须被契约套件判红")
	}
}

// versionResetBackend 复刻修复前 MemoryBackend 的删除语义（issue #181 的
// 原始缺陷形态）：Del 直接 delete(m.kv, key)，版本随条目一起消失——
// 同名键重建后版本从 VerMissing 重新 +1，旧代断言重新成立（ABA）。
type versionResetBackend struct{ inner *MemoryBackend }

func (b *versionResetBackend) Snapshot(ctx context.Context, keys ...string) (map[string]Entry, error) {
	return b.inner.Snapshot(ctx, keys...)
}

func (b *versionResetBackend) Txn(ctx context.Context, asserts []Assert, mutations []Mutation) (bool, error) {
	b.inner.mu.Lock()
	defer b.inner.mu.Unlock()
	for _, a := range asserts {
		e, ok := b.inner.kv[a.Key]
		cur := VerMissing
		if ok {
			cur = e.ver
		}
		if cur != a.Ver {
			return false, nil
		}
	}
	for _, mu := range mutations {
		if mu.Del {
			delete(b.inner.kv, mu.Key) // 缺陷本体：版本随条目删除
			continue
		}
		e, ok := b.inner.kv[mu.Key]
		if !ok {
			e = memEntry{ver: VerMissing}
		}
		e.ver++
		e.val = append([]byte(nil), mu.Val...)
		b.inner.kv[mu.Key] = e
	}
	return true, nil
}

func (b *versionResetBackend) Keys(ctx context.Context, prefix string) ([]string, error) {
	return b.inner.Keys(ctx, prefix)
}
