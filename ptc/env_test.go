package ptc

import (
	"context"
	"testing"
)

// TestRunEnvIsSnapshotBothWays RunEnv 的「快照」必须在注入侧与取出侧都成立
// （issue #389）。
//
// 只修一侧时另一个方向仍能改穿：注入方改一次工具名，EnvOf 取出来就是改后的
// 值；取出方改一次，再取一次也是改后的值。工具表是模型可调用工具的绑定面，
// 改穿等于「程序内可达 = 本环节模型可达」的恒等式失效。
func TestRunEnvIsSnapshotBothWays(t *testing.T) {
	env := RunEnv{Table: []ToolTableEntry{{Name: "alpha"}}}
	ctx := WithEnv(context.Background(), env)

	// 方向一：注入后改写调用方的表，ctx 里的快照不得变。
	env.Table[0].Name = "mutated-in"
	if got := EnvOf(ctx).Table[0].Name; got != "alpha" {
		t.Fatalf("注入侧未快照：改写调用方表写穿了 ctx，EnvOf=%q", got)
	}

	// 方向二：取出后改写拿到的表，再次取出不得变。
	got := EnvOf(ctx)
	got.Table[0].Name = "mutated-out"
	if again := EnvOf(ctx).Table[0].Name; again != "alpha" {
		t.Fatalf("取出侧未快照：改写返回值写穿了 ctx，再次 EnvOf=%q", again)
	}
}
