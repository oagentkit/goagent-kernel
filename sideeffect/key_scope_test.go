// key_scope_test.go —— 作用域段拼装的单射 / 稳定 / 透传判据（issue #261）。
package sideeffect

import (
	"slices"
	"testing"
)

// TestActionKeyBuilderInjective 不同身份（段集 × 叶段）拼不出同一个键。
//
// 每一对都是「天真拼法会撞、转义拼法不撞」的形态：段内的分隔符与叶段
// 的分段企图都被 %XX 转义挡下。撞键 = 两个逻辑动作共用一把幂等闸 =
// 互相重放/误伤——单射是身份判据的前提。
func TestActionKeyBuilderInjective(t *testing.T) {
	cases := []struct {
		name string
		a    func() string
		b    func() string
	}{
		{
			name: "段内分隔符 vs 更深的段",
			a:    func() string { return string(NewActionKeyBuilder("a/b").Key("c")) },
			b:    func() string { return string(NewActionKeyBuilder("a").Key("b/c")) },
		},
		{
			name: "叶段分段 vs 多一段的作用域",
			a:    func() string { return string(NewActionKeyBuilder("a", "b").Key("c/d")) },
			b:    func() string { return string(NewActionKeyBuilder("a", "b", "c").Key("d")) },
		},
		{
			name: "转义字符进段 vs 字面段",
			a:    func() string { return string(NewActionKeyBuilder("a%2Fb").Key("c")) },
			b:    func() string { return string(NewActionKeyBuilder("a/b").Key("c")) },
		},
		{
			name: "同段集不同叶",
			a:    func() string { return string(NewActionKeyBuilder("t", "r").Key("task:x")) },
			b:    func() string { return string(NewActionKeyBuilder("t", "r").Key("task:y")) },
		},
		{
			name: "同叶不同段集",
			a:    func() string { return string(NewActionKeyBuilder("t", "r1").Key("task:x")) },
			b:    func() string { return string(NewActionKeyBuilder("t", "r2").Key("task:x")) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.a() == tc.b() {
				t.Fatalf("两个不同身份拼出同一个键 %q——撞键即互相重放", tc.a())
			}
		})
	}
}

// TestActionKeyBuilderStable 拼装是纯函数：同一组输入恒得同一个键
// （重试稳定性由此成立；Attempt/时间戳之类的可变量不得进段——那是
// 调用方的构造错误，本机制不提供入口）。
func TestActionKeyBuilderStable(t *testing.T) {
	leaf := "task:deploy-01"
	want := string(NewActionKeyBuilder("t-a", "run-1").Key(leaf))
	for range 8 {
		if got := string(NewActionKeyBuilder("t-a", "run-1").Key(leaf)); got != want {
			t.Fatalf("同一身份必须拼出同一个键：%q != %q", got, want)
		}
	}
}

// TestActionKeyBuilderLegacyPassthrough 零作用域 ⇒ 叶段**原样**返回
// （不转义、不加前缀）：既有部署的默认键（"task:<id>"）与宿主自拼键
// 不因本机制迁移（零回归）。':' 是旧键的合法字符，转义它等于把全量
// 既有动作变成孤儿。
func TestActionKeyBuilderLegacyPassthrough(t *testing.T) {
	if got := string(NewActionKeyBuilder().Key("task:deploy-01")); got != "task:deploy-01" {
		t.Fatalf("零作用域必须逐字透传旧口径，实得 %q", got)
	}
	if got := NewActionKeyBuilder().Resource("deploy-01"); got != "deploy-01" {
		t.Fatalf("资源同样逐字透传，实得 %q", got)
	}
	// 显式空段 = 没给维度，与零作用域同口径。
	if got := string(NewActionKeyBuilder("", "").Key("task:deploy-01")); got != "task:deploy-01" {
		t.Fatalf("空段必须跳过（缺维度 ≠ 空维度），实得 %q", got)
	}
}

// TestActionKeyBuilderSkipsEmptySegments 空段不占身份位：["", "run-1"]
// 与 ["run-1"] 同键（否则「没给租户」会拼出一段空的、无法解释的身份）。
func TestActionKeyBuilderSkipsEmptySegments(t *testing.T) {
	withEmpty := string(NewActionKeyBuilder("", "run-1").Key("task:x"))
	without := string(NewActionKeyBuilder("run-1").Key("task:x"))
	if withEmpty != without {
		t.Fatalf("空段必须跳过：%q != %q", withEmpty, without)
	}
}

// TestActionKeyBuilderResourceParity 资源标识与键同一拼装口径：
// 有作用域时同样转义拼接（跨租户/跨 Run 不得互相抢资源）。
func TestActionKeyBuilderResourceParity(t *testing.T) {
	if got, want := NewActionKeyBuilder("t-a", "run-1").Resource("deploy-01"), "t-a/run-1/deploy-01"; got != want {
		t.Fatalf("资源应按同一口径拼装：%q != %q", got, want)
	}
	// 叶段转义：资源名里的分隔符不构成更深的段。
	if got := NewActionKeyBuilder("t-a").Resource("a/b"); got == "t-a/a/b" {
		t.Fatalf("资源叶段的分隔符必须转义（否则与更深的段集撞键）：%q", got)
	}
}

// TestActionKeyBuilderForIdentity 租户 + Scope 的统一入口（issue #275）：
// Tenant 排第一段、Scope 随后；与手拼同一组段逐字一致（单源拼装）。
func TestActionKeyBuilderForIdentity(t *testing.T) {
	act := Action{Tenant: "t-a", Scope: []string{"run-1", "env-prod"}}
	wantScopes := []string{"t-a", "run-1", "env-prod"}
	if got := act.Scopes(); !slices.Equal(got, wantScopes) {
		t.Fatalf("Scopes 顺序错误: %v，want %v", got, wantScopes)
	}
	forB := NewActionKeyBuilderFor(act.Tenant, act.Scope...)
	manual := NewActionKeyBuilder(wantScopes...)
	if got, want := string(forB.Key("task:deploy-01")), string(manual.Key("task:deploy-01")); got != want {
		t.Fatalf("NewActionKeyBuilderFor 与手拼必须同源: %q != %q", got, want)
	}
	if got, want := forB.Resource("deploy-01"), manual.Resource("deploy-01"); got != want {
		t.Fatalf("Resource 同源: %q != %q", got, want)
	}
	// 空租户不占身份位（缺维度 ≠ 空维度）。
	if got, want := string(NewActionKeyBuilderFor("", "run-1").Key("task:x")),
		string(NewActionKeyBuilder("run-1").Key("task:x")); got != want {
		t.Fatalf("空租户不得占段: %q != %q", got, want)
	}
	// Scopes 返回的切片不借用 Action.Scope 的底层数组。
	act.Scope[0] = "mutated"
	if act.Scopes()[1] != "mutated" {
		t.Fatal("Scopes 应反映 Action 当前值")
	}
	out := act.Scopes()
	out[0] = "hacked"
	if act.Scopes()[0] != "t-a" {
		t.Fatal("Scopes 的返回值不得改写 Action 的状态")
	}
}

// TestActionKeyBuilderEscapesSegments 转义方案与 coord 的租户段同构：
// 只保留 [A-Za-z0-9_-]，其余 %XX 大写十六进制（含 '%' 本身）。
func TestActionKeyBuilderEscapesSegments(t *testing.T) {
	cases := map[string]string{
		"a/b":  "a%2Fb",
		"a:b":  "a%3Ab",
		"a%b":  "a%25b",
		"租户-A": "%E7%A7%9F%E6%88%B7-A",
		"ok_1": "ok_1",
	}
	for in, want := range cases {
		if got := escapeKeySegment(in); got != want {
			t.Fatalf("escapeKeySegment(%q) = %q，want %q", in, got, want)
		}
	}
}
