package commitgate

// issue #204 的判据：回灌面不得吞 Store 故障。
//
// 旧实现 SeedE 在前置条件全部满足后 `_ = store.SeedIfAbsent(...)` 丢弃错误
// 仍返回 nil：多副本宿主启动对账逐行回灌时，Store 单行故障（连接重置/
// 序列化冲突/唯一键冲突）被吞——宿主认为回灌完成而**该环节的闸实际是空的**，
// 同一 (run, 环节) 的终态可被重复提交（副作用双写），且无日志、无计数、
// 返回 nil。errStore 替身早已在 store_test.go 里，却没有一条 SeedE × Store
// 故障用例——判据缺牙，坏实现全绿。

import (
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// TestSeedEReturnsErrorOnStoreFailure 反向用例（issue #204 验收一）：
// Store 故障时 SeedE 必须返回非 nil——「回灌失败」与「没有需要回灌的行」
// 必须可分（旧实现在此返回 nil，必红）。
func TestSeedEReturnsErrorOnStoreFailure(t *testing.T) {
	g := NewGuardWithStore(errStore{}, isTerminal)
	err := g.SeedE(Commit{Ref: ref("s1"), Status: "succeeded", Branch: "b", Seq: 1})
	if err == nil {
		t.Fatal("Store 故障时 SeedE 必须返回非 nil（吞掉 = 闸为空、重复终态提交重新敞开）")
	}
	if errors.Is(err, ErrEmptyRef) {
		t.Fatalf("存储故障不得被误报为 ErrEmptyRef: %v", err)
	}
	// 错误必须带定位上下文（宿主日志里能直接找到出错的行）。
	for _, want := range []string{"run-1", "s1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误应带 %q 上下文: %v", want, err)
		}
	}
}

// TestSeedEWrappedStoreErrorUnwraps SeedE 的错误链可解包：errors.Unwrap /
// Cause 能拿到 Store 原始故障（宿主判定连接类故障做退避重试靠它）。
func TestSeedEWrappedStoreErrorUnwraps(t *testing.T) {
	g := NewGuardWithStore(errStore{}, isTerminal)
	err := g.SeedE(Commit{Ref: ref("s1"), Status: "succeeded", Branch: "b", Seq: 1})
	if err == nil {
		t.Fatal("Store 故障必须报错")
	}
	if unwrapped := errors.Unwrap(err); unwrapped == nil || unwrapped.Error() != "store down" {
		t.Fatalf("错误链应能解到 Store 原始故障，实得 %v", unwrapped)
	}
}

// TestSeedWarnHookOnStoreFailure Seed 路径有留痕（issue #204 验收二）：
// Deprecated Seed 没有 error 通道，Store 故障经注入的 SeedWarn 钩子显式
// 报告——回灌失败与「没有需要回灌的行」从此可分。
func TestSeedWarnHookOnStoreFailure(t *testing.T) {
	g := NewGuardWithStore(errStore{}, isTerminal)
	var warned []Commit
	var causes []error
	g.WithSeedWarn(func(c Commit, cause error) {
		warned = append(warned, c)
		causes = append(causes, cause)
	})

	g.Seed(Commit{Ref: ref("s1"), Status: "succeeded", Branch: "b", Seq: 1}) // 旧实现静默

	if len(warned) != 1 {
		t.Fatalf("Store 故障时 Seed 留痕钩子必须被调用（实得 %d 次——静默 = 闸为空无人知）", len(warned))
	}
	if warned[0].Ref != (Ref{RunID: "run-1", Step: "s1"}) {
		t.Fatalf("留痕应带出回灌的环节，实得 %+v", warned[0])
	}
	if causes[0] == nil {
		t.Fatal("留痕必须带出 Store 故障原因")
	}
}

// TestSeedWarnNotCalledOnSkip 正常路径不触发留痕：非终态行跳过、空 Ref 跳过
// 都是「没有需要回灌的行」，不是失败——留痕只在真故障时响。
func TestSeedWarnNotCalledOnSkip(t *testing.T) {
	calls := 0
	hook := func(Commit, error) { calls++ }

	g := NewGuardWithStore(NewMemoryStore(), isTerminal).WithSeedWarn(hook)
	g.Seed(Commit{Ref: ref("s1"), Status: "running", Branch: "in-flight", Seq: 1}) // 非终态跳过
	g.Seed(Commit{Ref: Ref{RunID: "", Step: "s"}, Status: "succeeded", Seq: 1})    // 空 Ref 跳过

	if calls != 0 {
		t.Fatalf("跳过路径不得触发留痕（实得 %d 次）", calls)
	}
}

// TestSeedESucceedsOnHealthyStore 健康存储上 SeedE 仍返回 nil（语义不回退：
// 跳过非终态行是正常路径，不是失败）。
func TestSeedESucceedsOnHealthyStore(t *testing.T) {
	g := NewGuardWithStore(NewMemoryStore(), isTerminal)
	if err := g.SeedE(Commit{Ref: ref("s1"), Status: "running", Branch: "in-flight", Seq: 1}); err != nil {
		t.Fatalf("非终态行跳过是正常路径: %v", err)
	}
	if err := g.SeedE(Commit{Ref: ref("s2"), Status: "succeeded", Branch: "done", Seq: 2}); err != nil {
		t.Fatalf("健康存储回灌应成功: %v", err)
	}
	// 回灌生效：后续重复终态提交被挡（闸真的记上了——不是「没报错但没记上」）。
	if _, err := g.Acquire(Commit{Ref: ref("s2"), Status: "failed", Branch: "b2", Seq: 3}); !errors.Is(err, ErrAlreadyCommitted) {
		t.Fatalf("回灌的终态行应挡住后续提交，实得 %v", err)
	}
}
