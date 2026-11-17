// env_snapshot_test.go —— RunEnv 的「快照」必须真是快照（issue #389）。
//
// ctx 是**跨环节通道**（注入方 ≠ 读取方），而 RunEnv.Table 是切片：按值传
// 结构体只拷贝切片头，底层数组共享 ⇒ 两个方向都改穿：
//
//   - 注入方改写 env.Table[0].Name ⇒ EnvOf 取到的是改后的表；
//   - 读取方改写 got.Table[0].Name ⇒ 下一次 EnvOf 取到的也是改后的表。
//
// 工具表是**模型可调用工具**的绑定面：改穿后「程序内可达 = 本环节模型可达」
// 这条恒等式不成立，留痕里的工具表与实际绑定也对不上账。
package ptc

import (
	"context"
	"testing"
)

// TestWithEnvSnapshotsToolTable 入参侧：注入后改写入参，读到的必须不变。
func TestWithEnvSnapshotsToolTable(t *testing.T) {
	env := RunEnv{Table: []ToolTableEntry{{Name: "alpha"}}}
	ctx := WithEnv(context.Background(), env)

	env.Table[0].Name = "mutated-in"

	got := EnvOf(ctx)
	if len(got.Table) != 1 {
		t.Fatalf("工具表条数应为 1，实得 %d", len(got.Table))
	}
	if got.Table[0].Name != "alpha" {
		t.Fatalf("注入方改写入参后读到的工具名变了：实得 %q（应为 %q）——快照被改穿",
			got.Table[0].Name, "alpha")
	}
}

// TestEnvOfReturnsIndependentToolTable 出参侧：取出后改写，再取必须不变。
func TestEnvOfReturnsIndependentToolTable(t *testing.T) {
	env := RunEnv{Table: []ToolTableEntry{{Name: "alpha"}}}
	ctx := WithEnv(context.Background(), env)

	got := EnvOf(ctx)
	got.Table[0].Name = "mutated-out"

	again := EnvOf(ctx)
	if again.Table[0].Name != "alpha" {
		t.Fatalf("读取方改写后再次读到的工具名变了：实得 %q（应为 %q）——快照被改穿",
			again.Table[0].Name, "alpha")
	}
}
