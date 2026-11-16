package preset_test

// 预设集合的不可变性与语义键（issue #303 / #304）。
//
// #303：`Preset` 是值类型只挡住了字段替换，挡不住 `Egress.Allow` / `Bypass`
// 的切片头指向集合内部同一底层数组。三条路径（宿主原变量、Resolve 副本、
// All 快照）都能改穿集合，而 Set 的文档承诺的是「构造后只读，可并发共享」。
//
// #304：Key 把非代理模式下**不参与兑现**的维度也编进键（漏判「两个名字同一档」），
// 又用不转义的逗号拼接名单（把 `["a,b"]` 与 `["a","b"]` 判成同一份，撞名误拒）。

import (
	"fmt"
	"sync"
	"testing"

	"github.com/oagentkit/guardx/sandbox"
	"github.com/oagentkit/toolgate/policy"

	"github.com/yi-nology/goagent-kernel/egress"
	"github.com/yi-nology/goagent-kernel/preset"
)

func richEgressPreset() preset.Preset {
	return preset.Preset{
		Name:  "rich",
		Level: sandbox.LevelProcess,
		Mode:  policy.ModeAuto,
		Egress: egress.Spec{
			Mode:   egress.ModeProxy,
			Proxy:  "http://127.0.0.1:3128",
			Allow:  []string{"allow.example.com"},
			Bypass: []string{"bypass.example.com"},
		},
	}
}

// TestNewDeepCopiesIncomingPreset 构造后改宿主传进来的原变量，集合不得被改穿。
func TestNewDeepCopiesIncomingPreset(t *testing.T) {
	p := richEgressPreset()
	s := mustNew(t, p)

	p.Egress.Allow[0] = "mutated-by-host.example.com"
	p.Egress.Bypass[0] = "mutated-by-host.example.com"

	got, err := s.Resolve("rich")
	if err != nil {
		t.Fatal(err)
	}
	if got.Egress.Allow[0] != "allow.example.com" {
		t.Fatalf("宿主原变量改穿了集合 Allow：%v", got.Egress.Allow)
	}
	if got.Egress.Bypass[0] != "bypass.example.com" {
		t.Fatalf("宿主原变量改穿了集合 Bypass：%v", got.Egress.Bypass)
	}
}

// TestResolveReturnsDeepCopy Resolve 返回值原地改写后，再次 Resolve 必须逐字不变。
func TestResolveReturnsDeepCopy(t *testing.T) {
	s := mustNew(t, richEgressPreset())

	first, err := s.Resolve("rich")
	if err != nil {
		t.Fatal(err)
	}
	wantKey := first.Key()
	first.Egress.Allow[0] = "mutated-by-copy.example.com"
	first.Egress.Bypass[0] = "mutated-by-copy.example.com"

	again, err := s.Resolve("rich")
	if err != nil {
		t.Fatal(err)
	}
	if again.Egress.Allow[0] != "allow.example.com" || again.Egress.Bypass[0] != "bypass.example.com" {
		t.Fatalf("Resolve 副本改穿了集合：allow=%v bypass=%v", again.Egress.Allow, again.Egress.Bypass)
	}
	if again.Key() != wantKey {
		t.Fatalf("集合的语义键被改穿：%q vs %q", again.Key(), wantKey)
	}
}

// TestAllReturnsDeepCopy All 快照的元素切片也不得与集合共享底层数组。
func TestAllReturnsDeepCopy(t *testing.T) {
	s := mustNew(t, richEgressPreset())

	all := s.All()
	if len(all) != 1 {
		t.Fatalf("All 长度 = %d", len(all))
	}
	all[0].Egress.Allow[0] = "mutated-by-all.example.com"
	all[0].Egress.Bypass[0] = "mutated-by-all.example.com"

	got, err := s.Resolve("rich")
	if err != nil {
		t.Fatal(err)
	}
	if got.Egress.Allow[0] != "allow.example.com" || got.Egress.Bypass[0] != "bypass.example.com" {
		t.Fatalf("All 快照改穿了集合：allow=%v bypass=%v", got.Egress.Allow, got.Egress.Bypass)
	}
}

// TestConcurrentResolveAndMutationNoRace 并发 Resolve + 改写各自副本不得竞态；
// 同时集合的名单必须保持不变（配 -race 有效）。
func TestConcurrentResolveAndMutationNoRace(t *testing.T) {
	s := mustNew(t, richEgressPreset())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				p, err := s.Resolve("rich")
				if err != nil {
					t.Error(err)
					return
				}
				p.Egress.Allow[0] = fmt.Sprintf("writer-%d.example.com", i)
				p.Egress.Bypass[0] = fmt.Sprintf("writer-%d.example.com", i)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				p, err := s.Resolve("rich")
				if err != nil {
					t.Error(err)
					return
				}
				_ = p.Key()
				p.Egress.Allow[0] = "reader-copy.example.com"
			}
		}()
	}
	wg.Wait()

	got, err := s.Resolve("rich")
	if err != nil {
		t.Fatal(err)
	}
	if got.Egress.Allow[0] != "allow.example.com" || got.Egress.Bypass[0] != "bypass.example.com" {
		t.Fatalf("并发改写副本改穿了集合：allow=%v bypass=%v", got.Egress.Allow, got.Egress.Bypass)
	}
}

// TestKeyIgnoresLazyDimsOutsideProxyMode 非代理模式下 Proxy/Allow/Bypass 不参与
// 兑现（egress.Spec.Env 在 unrestricted 直接返回 base、在 denied 直接报错），
// 因而不得进入语义键：有效约束相同的两份预设必须被判为同一份并拒收。
func TestKeyIgnoresLazyDimsOutsideProxyMode(t *testing.T) {
	base := preset.Preset{
		Name:   "u-base",
		Level:  sandbox.LevelProcess,
		Mode:   policy.ModeAuto,
		Egress: egress.Spec{Mode: egress.ModeUnrestricted},
	}
	withAllow := base
	withAllow.Name = "u-allow"
	withAllow.Egress.Allow = []string{"only.example.com"}
	withProxy := base
	withProxy.Name = "u-proxy"
	withProxy.Egress.Proxy = "http://127.0.0.1:3128"
	withBypass := base
	withBypass.Name = "u-bypass"
	withBypass.Egress.Bypass = []string{"intra.example.com"}

	if base.Key() != withAllow.Key() || base.Key() != withProxy.Key() || base.Key() != withBypass.Key() {
		t.Fatalf("unrestricted 下惰性维度不得进键：base=%q allow=%q proxy=%q bypass=%q",
			base.Key(), withAllow.Key(), withProxy.Key(), withBypass.Key())
	}
	if _, err := preset.New([]preset.Preset{base, withAllow}); err == nil {
		t.Fatal("unrestricted 下有效约束相同的两份预设必须拒收")
	}

	deniedA := base
	deniedA.Name = "d-a"
	deniedA.Egress = egress.Spec{Mode: egress.ModeDenied, Allow: []string{"x.example.com"}}
	deniedB := base
	deniedB.Name = "d-b"
	deniedB.Egress = egress.Spec{
		Mode:   egress.ModeDenied,
		Proxy:  "http://127.0.0.1:3128",
		Bypass: []string{"y.example.com"},
	}
	if deniedA.Key() != deniedB.Key() {
		t.Fatalf("denied 下惰性维度不得进键：%q vs %q", deniedA.Key(), deniedB.Key())
	}
	if _, err := preset.New([]preset.Preset{deniedA, deniedB}); err == nil {
		t.Fatal("denied 下有效约束相同的两份预设必须拒收")
	}
}

// TestKeyEscapesListElements 代理模式下名单元素必须可逆转义：
// ["a,b"] 与 ["a","b"] 是真不同的白名单，不得判成同一份。
func TestKeyEscapesListElements(t *testing.T) {
	base := preset.Preset{
		Name:  "proxy-comma",
		Level: sandbox.LevelProcess,
		Mode:  policy.ModeAuto,
		Egress: egress.Spec{
			Mode:  egress.ModeProxy,
			Proxy: "http://127.0.0.1:3128",
		},
	}
	one := base
	one.Name = "one-element"
	one.Egress.Allow = []string{"a,b"}
	two := base
	two.Name = "two-elements"
	two.Egress.Allow = []string{"a", "b"}

	if one.Key() == two.Key() {
		t.Fatalf("逗号拼接把两份不同白名单撞成同一键：%q", one.Key())
	}
	if _, err := preset.New([]preset.Preset{one, two}); err != nil {
		t.Fatalf("真不同的白名单不得拒收: %v", err)
	}

	// 同一名单的排序/重复书写仍是同一份（集合语义不变）。
	dup := one
	dup.Name = "duplicate-element"
	dup.Egress.Allow = []string{"a,b", "a,b"}
	if one.Key() != dup.Key() {
		t.Fatalf("排序去重后应同键：%q vs %q", one.Key(), dup.Key())
	}
}
