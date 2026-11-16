package pii

// #296 判据先于实现：Policy 快照必须**可读**——「收到即所见」由访问方法兑现，
// 且快照读取与全局读取是两回事（读到的是收到的那一代）。
//
// 修复前：Policy 四个字段全部非导出且没有任何方法，订阅回调拿到的是哑值——
// 包头承诺「收到即所见」，USAGE 却自认「回调里只能调包级访问器读取」，而那一刻
// 可能已经换代，两份文档给出两种语义。
//
// 验收标准逐条：
//
//	订阅者读回调参数即读到那一代的策略/面开关/代次（本地性）；
//	Policy 无 setter、无可就地改写的导出字段（改不出全局）；
//	全局访问器与快照访问方法同在，二者口径在用例里显式对照。

import (
	"reflect"
	"testing"
)

// TestPolicySnapshotReadsLocalToGeneration 快照方法读的是**收到的那一代**，
// 不是此刻的全局——写侧换策略不得改写已到手快照的视图。
//
// 反向验证：把任一访问方法改成转读全局（curPolicy.Load()）⇒ 本用例转红。
func TestPolicySnapshotReadsLocalToGeneration(t *testing.T) {
	restore(t)

	if err := SetStrategy(StrategyRedact); err != nil {
		t.Fatal(err)
	}
	SetSurfaces(surfaceInputBit | surfaceOutputBit)

	snap := CurrentPolicy()
	before := snap.gen

	// 快照到手后写侧换代：全局应看到新代，快照必须停在收到的那一代。
	if err := SetStrategy(StrategyBlock); err != nil {
		t.Fatal(err)
	}
	SetEnabled(false)
	SetSurfaces(surfaceToolResultsBit)

	if snap.Strategy() != StrategyRedact {
		t.Fatalf("快照策略应停在收到的那一代（redact），实得 %q", snap.Strategy())
	}
	if Strategy() != StrategyBlock {
		t.Fatalf("全局策略应已换代（block），实得 %q", Strategy())
	}
	if !snap.Enabled() {
		t.Fatal("快照的全局开关应停在收到的那一代（开）")
	}
	if Enabled() {
		t.Fatal("全局开关应已关闭")
	}
	if snap.Surfaces() != surfaceInputBit|surfaceOutputBit {
		t.Fatalf("快照面集合应停在收到的那一代，实得 %d", snap.Surfaces())
	}
	if Surfaces() != surfaceToolResultsBit {
		t.Fatalf("全局面集合应已换代为仅 tool_results，实得 %d", Surfaces())
	}
	if !snap.SurfaceEnabled(SurfaceInput) || snap.SurfaceEnabled(SurfaceToolResults) {
		t.Fatal("快照的 SurfaceEnabled 应按快照的面集合判定")
	}
	if snap.Gen() != before {
		t.Fatalf("快照代次应停在收到的那一代：%d，实得 %d", before, snap.Gen())
	}
	if Gen() <= before {
		t.Fatalf("全局代次应已前进超过快照代次 %d，实得 %d", before, Gen())
	}
}

// TestPolicyDigestKeyedReadsSnapshot hash 档密钥的有无随快照走：密钥不进
// 只读面（密钥是机密，可读面只暴露「有没有」）。
func TestPolicyDigestKeyedReadsSnapshot(t *testing.T) {
	restore(t)

	snap := CurrentPolicy()
	if snap.DigestKeyed() {
		t.Fatal("未注入密钥时快照 DigestKeyed 应为假")
	}
	if err := SetDigestKey("0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if !DigestKeyed() {
		t.Fatal("注入后全局 DigestKeyed 应为真")
	}
	// 修复前取的快照与注入后的全局无关——这里重取一份验证注入面本身；
	// 旧快照仍应报告无密钥（它那一代确实没有）。
	if snap.DigestKeyed() {
		t.Fatal("旧快照不得读到注入后的密钥状态")
	}
	fresh := CurrentPolicy()
	if !fresh.DigestKeyed() {
		t.Fatal("注入后新快照 DigestKeyed 应为真")
	}
}

// TestPolicySnapshotSurfaceSet 排查「快照方法与全局访问器同名」的歧义：
// 两套同名共存是有意设计——快照方法绑定快照实例，全局访问器读当前代，
// 用例并排对照两者在换代前后的取值。
func TestPolicySnapshotVsGlobalAccessors(t *testing.T) {
	restore(t)

	if err := SetStrategy(StrategyHash); err != nil {
		t.Fatal(err)
	}
	snap := CurrentPolicy()
	if err := SetStrategy(StrategyMask); err != nil {
		t.Fatal(err)
	}
	if snap.Strategy() == Strategy() {
		t.Fatalf("快照（%q）与全局（%q）应各读各的代", snap.Strategy(), Strategy())
	}
	if snap.Strategy() != StrategyHash || Strategy() != StrategyMask {
		t.Fatalf("快照应 hash、全局应 mask，实得 %q / %q", snap.Strategy(), Strategy())
	}
}

// TestPolicyHasNoExportedFields Policy 不得有导出字段：外部调用方拿到的
// 快照值**无处可改**，CurrentPolicy() 的返回值因此不可能被就地改出全局。
//
// 同时钉住方法面：只读访问方法的集合一旦有人塞进 setter，本用例当场转红。
func TestPolicyHasNoExportedFields(t *testing.T) {
	tp := reflect.TypeOf(Policy{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.PkgPath == "" {
			t.Fatalf("Policy 不得有导出字段（就地改写即可改出全局）：%s", f.Name)
		}
	}

	readonlyMethods := map[string]bool{
		"Enabled": true, "Surfaces": true, "SurfaceEnabled": true,
		"Strategy": true, "Gen": true, "DigestKeyed": true,
	}
	for i := 0; i < tp.NumMethod(); i++ {
		m := tp.Method(i)
		if !readonlyMethods[m.Name] {
			t.Fatalf("Policy 方法面只允许只读访问方法，发现 %q——任何 setter 都会让快照可被外部改写", m.Name)
		}
		delete(readonlyMethods, m.Name)
	}
	for name := range readonlyMethods {
		t.Fatalf("只读访问方法 %q 缺失——快照不可读，「收到即所见」无从谈起", name)
	}
}
