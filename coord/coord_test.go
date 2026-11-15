package coord

import (
	"context"
	"testing"

	"github.com/oekit/pkg/errors"
)

// 测试夹具全部合成数据（AGENTS.md 红线）。

func TestMemoryBackendTxnAllOrNothing(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryBackend()
	if _, err := b.Txn(ctx, nil, []Mutation{
		{Key: "k1", Val: []byte("v1")},
	}); err != nil {
		t.Fatal(err)
	}
	// 断言失败 ⇒ 全部写入不执行（包括那条本来能成的）。
	// 断言对象是 k1 的一个**不存在的历史版本**（k1 当前 ver=1，断言 5）。
	applied, err := b.Txn(ctx, []Assert{{Key: "k1", Ver: Version(5)}}, []Mutation{
		{Key: "k1", Val: []byte("WRONG")},
		{Key: "k2", Val: []byte("new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("断言失败的事务不得提交")
	}
	snap2, err := b.Snapshot(ctx, "k1", "k2")
	if err != nil {
		t.Fatal(err)
	}
	if string(snap2["k1"].Val) != "v1" {
		t.Fatalf("all-or-nothing 被破坏：k1 被部分写入（实得 %s）", snap2["k1"].Val)
	}
	if snap2["k2"].Exists {
		t.Fatal("all-or-nothing 被破坏：k2 被部分写入")
	}
}

func TestMemoryBackendVersionMonotone(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryBackend()
	for i := 1; i <= 3; i++ {
		applied, err := b.Txn(ctx, nil, []Mutation{{Key: "k", Val: []byte("v")}})
		if err != nil || !applied {
			t.Fatalf("第 %d 次写入应成功", i)
		}
		snap, _ := b.Snapshot(ctx, "k")
		if snap["k"].Ver != Version(i) {
			t.Fatalf("版本必须逐次 +1（第 %d 次后实得 %d）", i, snap["k"].Ver)
		}
	}
	// 删除即转墓碑（issue #181——旧口径「删除即丢版本」被判契约违反）：
	// Exists=false 但版本保留并推进——版本是断言的锚点，随条目一起删掉
	// 就是把它拆了（同名键重建后旧版本断言会误判「没动过」= ABA）。
	if _, err := b.Txn(ctx, nil, []Mutation{{Key: "k", Del: true}}); err != nil {
		t.Fatal(err)
	}
	snap, _ := b.Snapshot(ctx, "k")
	if snap["k"].Exists || snap["k"].Ver != Version(4) {
		t.Fatalf("删除后应不存在且版本保留墓碑值（实得 %+v）", snap["k"])
	}
	// 重建：版本继续前进，不复用历史；删除前的旧版本断言必失败。
	if _, err := b.Txn(ctx, nil, []Mutation{{Key: "k", Val: []byte("v2")}}); err != nil {
		t.Fatal(err)
	}
	snap, _ = b.Snapshot(ctx, "k")
	if !snap["k"].Exists || snap["k"].Ver != Version(5) {
		t.Fatalf("重建后版本必须继续前进（实得 %+v）", snap["k"])
	}
	applied, err := b.Txn(ctx, []Assert{{Key: "k", Ver: Version(1)}}, []Mutation{{Key: "k", Val: []byte("v3")}})
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("删除前旧版本的断言必须失败（ABA 防线——版本不复用）")
	}
}

func TestMemoryBackendKeysPrefix(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryBackend()
	_, err := b.Txn(ctx, nil, []Mutation{
		{Key: "coord/task/a", Val: []byte("1")},
		{Key: "coord/task/b", Val: []byte("2")},
		{Key: "coord/act/a", Val: []byte("3")},
		{Key: "other", Val: []byte("4")},
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := b.Keys(ctx, "coord/task/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "coord/task/a" || keys[1] != "coord/task/b" {
		t.Fatalf("前缀列键不符（实得 %v）", keys)
	}
}

func TestConcurrentTxnExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	if err := ConcurrentTxn(ctx, NewMemoryBackend(), "judge-key", 16); err != nil {
		t.Fatalf("内存后端的原子性判据必须绿（实得 %v）", err)
	}
	if err := ConcurrentTxn(ctx, NewMemoryBackend(), "judge-empty", 2); err != nil {
		t.Fatalf("对不存在的键同样成立（实得 %v）", err)
	}
}

func TestConcurrentTxnRejectsBadArity(t *testing.T) {
	if err := ConcurrentTxn(context.Background(), NewMemoryBackend(), "k", 1); err == nil {
		t.Fatal("单竞争者的判据没有测到争用，必须拒绝")
	}
}

// brokenBackend 坏实现的替身：断言**校验与写入分离**（先查后写、没有原子性）。
//
// 反向验证（AGENTS.md 纪律：判据必须有「坏实现会红」的用例）：它模拟的正是
// sidetest 包头警告的头号失效形态——ConcurrentTxn 对它必须红。
type brokenBackend struct{ inner *MemoryBackend }

func (b *brokenBackend) Snapshot(ctx context.Context, keys ...string) (map[string]Entry, error) {
	return b.inner.Snapshot(ctx, keys...)
}

func (b *brokenBackend) Txn(ctx context.Context, _ []Assert, mutations []Mutation) (bool, error) {
	// 断言被忽略（空 If 的 txn / 无条件 Lua 的形态）：写入不带任何条件——
	// 「断言与写入未原子」最直接的一种坏法。
	_, err := b.inner.Txn(ctx, nil, mutations)
	return true, err
}

func (b *brokenBackend) Keys(ctx context.Context, prefix string) ([]string, error) {
	return b.inner.Keys(ctx, prefix)
}

func (b *brokenBackend) ScanSnapshot(ctx context.Context, prefix string) (map[string]Entry, error) {
	return b.inner.ScanSnapshot(ctx, prefix)
}

func TestConcurrentTxnRedOnBrokenBackend(t *testing.T) {
	// 坏实现：两个竞争者都通过「检查」，先后写入 ⇒ 多个赢家 ⇒ 判据红。
	err := ConcurrentTxn(context.Background(), &brokenBackend{inner: NewMemoryBackend()}, "k", 8)
	if err == nil {
		t.Fatal("断言与写入分离的坏实现必须被 ConcurrentTxn 判红——判据没牙齿等于没有判据")
	}
}

func TestCASLoopConflictExhausted(t *testing.T) {
	// 永远冲突的后端：CAS 循环触顶必须显式报 ErrTxnConflict，不挂死。
	b := conflictBackend{}
	_, err := casLoop(context.Background(), b, []string{"k"},
		func(snap map[string]Entry) (string, []Assert, []Mutation, error) {
			return "x", []Assert{{Key: "k", Ver: snap["k"].Ver}}, []Mutation{{Key: "k", Val: []byte("v")}}, nil
		})
	if !errors.Is(err, ErrTxnConflict) {
		t.Fatalf("触顶必须报 ErrTxnConflict（实得 %v）", err)
	}
}

// conflictBackend 恒返回 applied=false 的替身（高争用的确定性构造）。
type conflictBackend struct{}

func (conflictBackend) Snapshot(_ context.Context, keys ...string) (map[string]Entry, error) {
	out := map[string]Entry{}
	for _, k := range keys {
		out[k] = Entry{Key: k, Ver: VerMissing}
	}
	return out, nil
}

func (conflictBackend) Txn(_ context.Context, _ []Assert, _ []Mutation) (bool, error) {
	return false, nil
}

func (conflictBackend) Keys(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (conflictBackend) ScanSnapshot(_ context.Context, _ string) (map[string]Entry, error) {
	return map[string]Entry{}, nil
}

func TestCASLoopPlanErrorAborts(t *testing.T) {
	// 判定类失败（ErrTaken 等）是结论不是冲突：plan 报错即终止，不重试。
	runs := 0
	b := countingBackend{onTxn: func() { runs++ }}
	_, err := casLoop(context.Background(), b, []string{"k"},
		func(snap map[string]Entry) (string, []Assert, []Mutation, error) {
			return "", nil, nil, errors.New("coord: 判定类失败（替身）")
		})
	if err == nil {
		t.Fatal("plan 的判定类失败必须原样上抛")
	}
	if runs != 0 {
		t.Fatalf("判定类失败不得触发事务（实得 %d 次）", runs)
	}
}

// countingBackend 记录 Txn 调用次数的替身。
type countingBackend struct{ onTxn func() }

func (c countingBackend) Snapshot(_ context.Context, keys ...string) (map[string]Entry, error) {
	out := map[string]Entry{}
	for _, k := range keys {
		out[k] = Entry{Key: k, Ver: VerMissing}
	}
	return out, nil
}

func (c countingBackend) Txn(_ context.Context, _ []Assert, _ []Mutation) (bool, error) {
	c.onTxn()
	return true, nil
}

func (c countingBackend) Keys(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c countingBackend) ScanSnapshot(_ context.Context, _ string) (map[string]Entry, error) {
	return map[string]Entry{}, nil
}

// TestVersionNotReusedOnMemoryBackend 契约判据对参考实现自检（issue #181）：
// 写 → 删 → 重建 → 旧版本 CAS 必须失败。宿主的 Redis/etcd/PG Backend
// 实现对同一判据跑同一套（coord.VersionNotReused）。
func TestVersionNotReusedOnMemoryBackend(t *testing.T) {
	ctx := context.Background()
	if err := VersionNotReused(ctx, NewMemoryBackend(), "aba-key"); err != nil {
		t.Fatalf("版本单调契约判据必须过： %v", err)
	}
}

// TestStaleVersionAssertFailsAfterRebuild 钉 ABA 场景本身（判据背后的事故
// 形态）：快照读到 k@1 ⇒ 另一路删 k ⇒ 重建 k ⇒ 旧断言 k@1 必须失败——
// 旧实现里版本从 1 重来、断言重新成立，判定读的是上一代的值、提交写的
// 是新一代的键。
func TestStaleVersionAssertFailsAfterRebuild(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryBackend()

	if applied, err := b.Txn(ctx, nil, []Mutation{{Key: "k", Val: []byte("上一代")}}); err != nil || !applied {
		t.Fatalf("首写失败: %v %v", applied, err)
	}
	snap, _ := b.Snapshot(ctx, "k")
	stale := snap["k"].Ver

	if applied, err := b.Txn(ctx, nil, []Mutation{{Key: "k", Del: true}}); err != nil || !applied {
		t.Fatalf("删除失败: %v %v", applied, err)
	}
	snap, _ = b.Snapshot(ctx, "k")
	if snap["k"].Exists {
		t.Fatal("删除后键必须不存在")
	}
	if snap["k"].Ver != stale+1 {
		t.Fatalf("删除必须推进版本（墓碑）： %d → %d", stale, snap["k"].Ver)
	}

	if applied, err := b.Txn(ctx, nil, []Mutation{{Key: "k", Val: []byte("新一代")}}); err != nil || !applied {
		t.Fatalf("重建失败: %v %v", applied, err)
	}
	snap, _ = b.Snapshot(ctx, "k")
	if snap["k"].Ver <= stale {
		t.Fatalf("重建后版本不得回退/复用： %d → %d", stale, snap["k"].Ver)
	}

	// 快照里那份「k@旧版本」的断言对新一代的键必须不成立。
	applied, err := b.Txn(ctx, []Assert{{Key: "k", Ver: stale}}, []Mutation{{Key: "k", Val: []byte("ABA 注入")}})
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatalf("旧版本 %d 的断言在重建后的键上提交成功——ABA 防线破", stale)
	}
	snap, _ = b.Snapshot(ctx, "k")
	if string(snap["k"].Val) != "新一代" {
		t.Fatalf("失败断言的事务不得写入（实得 %s）", snap["k"].Val)
	}
}
