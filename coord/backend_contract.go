// backend_contract.go —— Backend 的**可复用契约套件**（导出判据，宿主与适配器共用）。
//
// # 为什么是可跑判据
//
// Backend 的三条硬要求（Txn 原子 / Snapshot 一致 / Version 单调）是「宿主实现
// Redis / etcd / PG 后端」的准入条件。散文写在接口注释里没人跑；判据写成
// 导出函数，宿主在自家测试里跑一遍即知是否合规——与 ConcurrentTxn、
// taskboard.ConcurrentClaim、sidetest / boardtest 同一条纪律：
// 判据归内核，实现方只负责让原语成立。
//
// # 删除推进版本（issue #181）
//
// 版本是断言的锚点，锚点必须**跨删除存活**：Del 若直接丢条目，同名键重建后
// 版本从 VerMissing 重新 +1 ⇒ 旧代快照的断言 k@1 在「删 + 重建」后重新成立
// ——ABA 误判：旧代判定读的是上一代的值，提交写的是新一代的键。本套件的核心
// 负向用例即：写 k（v1）→ 删 k（墓碑 v2）→ 重建 k（v3）→ 用 v1 的 CAS 必须失败。
//
// # 墓碑不得随意 GC
//
// 套件同时判「删过的键版本不得回到 VerMissing」：墓碑一旦被回收，同名键重建
// 又回到版本 1，ABA 窗口重新打开。GC 的许可条件写在 Backend 接口文档的
// Version 条款里（本仓两个参考实现都不 GC）。
package coord

import (
	"context"
	"fmt"
	"slices"
)

// BackendContract 跑 Backend 契约套件的全部判据，全过才算接上协调契约。
//
// ns 是套件**独占**的键空间前缀：套件在 ns 下写入、删除自己的键，并对
// ns 下的前缀列键结果做全量断言——调用方必须保证 ns 下没有别人的键
// （每次运行换一个前缀，通常用测试名）。
//
// 判据清单：
//
//   - 版本推进与墓碑形态：每次成功写入 +1；Del 也是一次成功写入（版本 +1、
//     Exists=false、值清空）；Del 从未存在的键是空操作（不创建墓碑、不消耗
//     版本）；墓碑上断言 VerMissing 必须失败；
//   - 删后重建的 ABA 负向用例：旧代版本的 CAS 必须失败（issue #181）；
//   - 墓碑不出现在前缀列键结果里；
//   - 并发断言恰好一个赢家、版本恰好 +1（ConcurrentTxn 判据）。
//
// 返回的 error 只表示**契约违反**（或后端基础设施故障），不是「某个用例
// 没测到争用」之类的软结论——套件不静默跳过任何一条。
func BackendContract(ctx context.Context, b Backend, ns string) error {
	if b == nil {
		return fmt.Errorf("coord: Backend 契约套件收到 nil 后端")
	}
	if ns == "" {
		return fmt.Errorf("coord: Backend 契约套件需要非空的键空间前缀（套件在它下面写入并全量列键）")
	}
	checks := []struct {
		name string
		run  func() error
	}{
		{"删除推进版本且条目转墓碑", func() error { return checkDeleteKeepsVersion(ctx, b, ns) }},
		{"删后重建旧版本 CAS 必须失败", func() error { return checkStaleAssertAfterRecreate(ctx, b, ns) }},
		{"墓碑不出现在前缀列键里", func() error { return checkTombstoneHiddenFromKeys(ctx, b, ns) }},
		{"并发断言恰好一个赢家", func() error { return checkConcurrentAssertSingleWinner(ctx, b, ns) }},
	}
	for _, c := range checks {
		if err := c.run(); err != nil {
			return fmt.Errorf("coord: Backend 契约不成立 [%s]: %w", c.name, err)
		}
	}
	return nil
}

// checkDeleteKeepsVersion 版本跨删除存活：写入逐次 +1，删除也是一次写入。
//
// 期望用相对版本（先写一次取当前版本，再判后续递增）而不是硬编码 1：
// 同一后端上换 ns 重跑同样成立（键名在 ns 下唯一，版本从后端当前值起算）。
func checkDeleteKeepsVersion(ctx context.Context, b Backend, ns string) error {
	key := ns + "lifecycle"
	if err := mustApply(ctx, b, nil, []Mutation{{Key: key, Val: []byte("gen-1")}}, "首次写入"); err != nil {
		return err
	}
	first, err := snapshotOne(ctx, b, key)
	if err != nil {
		return err
	}
	if !first.Exists || string(first.Val) != "gen-1" {
		return fmt.Errorf("首次写入后条目必须存在（实得 Exists=%v Val=%q）", first.Exists, first.Val)
	}
	v1 := first.Ver

	// 第二次写入：版本 v1+1（证明写入逐次 +1）。
	if err := mustApply(ctx, b, []Assert{{Key: key, Ver: v1}}, []Mutation{{Key: key, Val: []byte("gen-2")}}, "第二次写入"); err != nil {
		return err
	}
	// 删除：版本 v1+2，条目转墓碑。
	if err := mustApply(ctx, b, []Assert{{Key: key, Ver: v1 + 1}}, []Mutation{{Key: key, Del: true}}, "删除"); err != nil {
		return err
	}
	gone, err := snapshotOne(ctx, b, key)
	if err != nil {
		return err
	}
	if gone.Exists {
		return fmt.Errorf("删除后条目必须不存在（实得 Exists=true Val=%q）", gone.Val)
	}
	if gone.Ver != v1+2 {
		return fmt.Errorf("删除也是一次成功写入：版本必须推进到 %d（实得 %d）——版本随条目一起消失就是 ABA 窗口", v1+2, gone.Ver)
	}
	if len(gone.Val) != 0 {
		return fmt.Errorf("墓碑不得保留值（实得 %q）", gone.Val)
	}

	// 墓碑上断言 VerMissing 必须失败：放行会让同名键的首次写入复用墓碑之前的版本。
	applied, err := b.Txn(ctx, []Assert{{Key: key, Ver: VerMissing}}, []Mutation{{Key: key, Val: []byte("reuse-old-version")}})
	if err != nil {
		return fmt.Errorf("墓碑上的 VerMissing 断言：事务报错: %w", err)
	}
	if applied {
		return fmt.Errorf("墓碑上不得成立 VerMissing 断言——放行会让重建的键复用旧版本（版本不复用的契约破了）")
	}

	// 重建：版本继续 +1（从墓碑之后，不回退）。
	if err := mustApply(ctx, b, []Assert{{Key: key, Ver: v1 + 2}}, []Mutation{{Key: key, Val: []byte("gen-3")}}, "重建"); err != nil {
		return err
	}
	back, err := snapshotOne(ctx, b, key)
	if err != nil {
		return err
	}
	if !back.Exists || back.Ver != v1+3 || string(back.Val) != "gen-3" {
		return fmt.Errorf("重建必须从墓碑版本之后继续（期望 Exists=true Ver=%d Val=%q，实得 Exists=%v Ver=%d Val=%q）",
			v1+3, "gen-3", back.Exists, back.Ver, back.Val)
	}

	// 删除从未存在的键：空操作（不创建墓碑、不消耗版本）。
	ghost := ns + "never-existed"
	if err := mustApply(ctx, b, nil, []Mutation{{Key: ghost, Del: true}}, "删除从未存在的键"); err != nil {
		return err
	}
	ghostEntry, err := snapshotOne(ctx, b, ghost)
	if err != nil {
		return err
	}
	if ghostEntry.Exists || ghostEntry.Ver != VerMissing {
		return fmt.Errorf("删除从未存在的键必须是空操作（不得创建墓碑或消耗版本；实得 Exists=%v Ver=%d）",
			ghostEntry.Exists, ghostEntry.Ver)
	}
	return nil
}

// checkStaleAssertAfterRecreate issue #181 的核心负向用例：
// 写 → 删 → 重建 → 旧代版本的 CAS 必须失败。
func checkStaleAssertAfterRecreate(ctx context.Context, b Backend, ns string) error {
	key := ns + "aba"
	if err := mustApply(ctx, b, nil, []Mutation{{Key: key, Val: []byte("gen-a")}}, "旧代写入"); err != nil {
		return err
	}
	old, err := snapshotOne(ctx, b, key)
	if err != nil {
		return err
	}
	if !old.Exists {
		return fmt.Errorf("旧代写入后条目必须存在")
	}
	staleVer := old.Ver // 快照断言用它（旧代版本）

	// 另一路：删除 + 重建（版本必须离开 staleVer）。
	if err := mustApply(ctx, b, []Assert{{Key: key, Ver: staleVer}}, []Mutation{{Key: key, Del: true}}, "删除旧代"); err != nil {
		return err
	}
	if err := mustApply(ctx, b, []Assert{{Key: key, Ver: staleVer + 1}}, []Mutation{{Key: key, Val: []byte("gen-b")}}, "重建新代"); err != nil {
		return err
	}

	// 旧代断言（staleVer）不得重新成立——版本复用就是 ABA。
	applied, err := b.Txn(ctx, []Assert{{Key: key, Ver: staleVer}}, []Mutation{{Key: key, Val: []byte("stale-writer")}})
	if err != nil {
		return fmt.Errorf("旧代版本 CAS：事务报错: %w", err)
	}
	if applied {
		return fmt.Errorf("旧代版本 %d 的 CAS 在「删除 + 重建」后重新成立——ABA 误判（旧代判定会放行新一代的键）", staleVer)
	}

	// 失败的事务不得留下任何写入：新一代的值与版本必须原样。
	after, err := snapshotOne(ctx, b, key)
	if err != nil {
		return err
	}
	if !after.Exists || string(after.Val) != "gen-b" || after.Ver != staleVer+2 {
		return fmt.Errorf("被拒的旧代事务不得写入（期望 Exists=true Ver=%d Val=%q，实得 Exists=%v Ver=%d Val=%q）",
			staleVer+2, "gen-b", after.Exists, after.Ver, after.Val)
	}
	return nil
}

// checkTombstoneHiddenFromKeys 墓碑是「已删除」：前缀列键只列活键。
func checkTombstoneHiddenFromKeys(ctx context.Context, b Backend, ns string) error {
	prefix := ns + "keys/"
	live, dead := prefix+"live", prefix+"dead"
	if err := mustApply(ctx, b, nil, []Mutation{
		{Key: live, Val: []byte("1")},
		{Key: dead, Val: []byte("2")},
	}, "摆入活键与待删键"); err != nil {
		return err
	}
	if err := mustApply(ctx, b, nil, []Mutation{{Key: dead, Del: true}}, "删除待删键"); err != nil {
		return err
	}
	keys, err := b.Keys(ctx, prefix)
	if err != nil {
		return fmt.Errorf("前缀列键失败: %w", err)
	}
	if !slices.Contains(keys, live) {
		return fmt.Errorf("前缀列键漏掉活键 %q（实得 %v）", live, keys)
	}
	if slices.Contains(keys, dead) {
		return fmt.Errorf("前缀列键列出了墓碑 %q（删除的键不得出现；实得 %v）", dead, keys)
	}
	return nil
}

// checkConcurrentAssertSingleWinner 并发原子性判据的套件接线（并发规模 8）。
func checkConcurrentAssertSingleWinner(ctx context.Context, b Backend, ns string) error {
	const workers = 8
	if err := ConcurrentTxn(ctx, b, ns+"concurrent", workers); err != nil {
		return fmt.Errorf("并发原子性判据未过: %w", err)
	}
	return nil
}

// mustApply 提交一批写入并断言其成功（判据内部的失败信息带上下文）。
func mustApply(ctx context.Context, b Backend, asserts []Assert, muts []Mutation, what string) error {
	applied, err := b.Txn(ctx, asserts, muts)
	if err != nil {
		return fmt.Errorf("%s: 事务报错: %w", what, err)
	}
	if !applied {
		return fmt.Errorf("%s: 断言成立却未提交（applied=false）", what)
	}
	return nil
}

// snapshotOne 读单个键并核对快照返回了该键。
func snapshotOne(ctx context.Context, b Backend, key string) (Entry, error) {
	snap, err := b.Snapshot(ctx, key)
	if err != nil {
		return Entry{}, fmt.Errorf("快照读 %q 失败: %w", key, err)
	}
	e, ok := snap[key]
	if !ok {
		return Entry{}, fmt.Errorf("快照结果缺少键 %q", key)
	}
	return e, nil
}
