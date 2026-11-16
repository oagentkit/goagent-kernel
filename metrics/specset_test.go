package metrics

import (
	"testing"

	"github.com/oekit/pkg/errors"
)

// hostMetric 合成数据：宿主自有指标（前缀不是 harness.，也不在内核登记表里）。
func hostMetric() Metric {
	return Metric{
		Name: "host.queue.depth",
		Kind: KindGauge,
		Unit: "1",
		Desc: "宿主自有队列的瞬时深度",
		Labels: []LabelDim{{
			Key:    "host.queue.name",
			Values: []string{"ingress", "egress"},
			Why:    "队列是封闭枚举；新增队列必须同时改这份登记，基数才不会无界",
		}},
	}
}

// hostSecondMetric 第二个宿主指标（Merge 用例用）。
func hostSecondMetric() Metric {
	return Metric{
		Name: "host.worker.inflight",
		Kind: KindCounter,
		Unit: "1",
		Desc: "宿主自有的在途 worker 计数",
		Labels: []LabelDim{{
			Key:    "host.worker.pool",
			Values: []string{"pool-a", "pool-b"},
			Why:    "池名是部署期封闭集合",
		}},
	}
}

// TestSpecSetRegisterAndValidate 钉住 issue #239 的主判据：宿主注册自有 Specs
// 之后，Validate 的登记表判据与标签基数门对它生效；包级默认表不被改写。
func TestSpecSetRegisterAndValidate(t *testing.T) {
	before := len(Specs())
	base := DefaultSpecs()
	if base.Len() != before {
		t.Fatalf("DefaultSpecs() 条数 = %d, want %d", base.Len(), before)
	}

	set, err := base.Register(hostMetric())
	if err != nil {
		t.Fatalf("注册宿主指标: %v", err)
	}
	if set.Len() != before+1 {
		t.Fatalf("注册后条数 = %d, want %d", set.Len(), before+1)
	}

	// 宿主指标的合法埋点：登记项 + 有界取值通过。
	if err := set.Validate(hostMetric().Name, Labels{"host.queue.name": "ingress"}); err != nil {
		t.Fatalf("宿主指标合法埋点被判违规: %v", err)
	}
	if err := set.Validate(hostMetric().Name, nil); err != nil {
		t.Fatalf("不带标签的宿主指标应合法: %v", err)
	}
	// 越界取值 / 未声明标签键：沿用同一套基数门。
	if err := set.Validate(hostMetric().Name, Labels{"host.queue.name": "elsewhere"}); err == nil {
		t.Fatal("越界取值应被拒")
	}
	if err := set.Validate(hostMetric().Name, Labels{"host.queue.zone": "z1"}); err == nil {
		t.Fatal("未声明的标签键应被拒")
	}
	if err := set.Validate("host.not.registered", nil); !errors.Is(err, ErrUnregisteredMetric) {
		t.Fatalf("该表内未登记的指标名应报 ErrUnregisteredMetric, got %v", err)
	}

	// 包级默认表与 base 都不受影响（Register 返回新表，Clone 语义）。
	if len(Specs()) != before {
		t.Fatalf("注册宿主指标污染了包级登记表: %d -> %d", before, len(Specs()))
	}
	if base.Len() != before {
		t.Fatalf("注册宿主指标污染了 DefaultSpecs 副本: %d -> %d", before, base.Len())
	}
	if _, ok := SpecOf(hostMetric().Name); ok {
		t.Fatal("包级 SpecOf 不应看见宿主指标")
	}
	if _, ok := base.SpecOf(hostMetric().Name); ok {
		t.Fatal("DefaultSpecs 副本不应看见宿主指标")
	}
	if err := Validate(hostMetric().Name, Labels{"host.queue.name": "ingress"}); !errors.Is(err, ErrUnregisteredMetric) {
		t.Fatalf("包级 Validate 不应接受宿主指标（默认表未被篡改）, got %v", err)
	}

	// 零值 SpecSet 可用：宿主不必先拿默认表。
	var empty SpecSet
	if empty.Len() != 0 {
		t.Fatalf("零值 SpecSet 条数 = %d, want 0", empty.Len())
	}
	only, err := empty.Register(hostSecondMetric())
	if err != nil {
		t.Fatalf("零值 SpecSet 注册: %v", err)
	}
	if err := only.Validate(hostSecondMetric().Name, Labels{"host.worker.pool": "pool-a"}); err != nil {
		t.Fatalf("零值 SpecSet 注册后的校验: %v", err)
	}
}

// TestSpecSetRegisterRejectsBadSpec 登记项自身不自洽 / 重名 / 禁区标签一律拒收，
// 且失败不改变原表。
func TestSpecSetRegisterRejectsBadSpec(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Metric)
	}{
		{"缺指标名", func(m *Metric) { m.Name = "" }},
		{"非法类型", func(m *Metric) { m.Kind = "summary" }},
		{"缺单位", func(m *Metric) { m.Unit = "" }},
		{"缺语义说明", func(m *Metric) { m.Desc = "" }},
		{"缺标签键", func(m *Metric) { m.Labels[0].Key = "" }},
		{"无取值域（无界基数）", func(m *Metric) { m.Labels[0].Values = nil }},
		{"缺基数上界依据", func(m *Metric) { m.Labels[0].Why = "" }},
		{"取值重复", func(m *Metric) { m.Labels[0].Values = []string{"same", "same"} }},
		{"标签键重复", func(m *Metric) {
			m.Labels = append(m.Labels, m.Labels[0])
		}},
		{"禁区标签", func(m *Metric) {
			m.Labels[0].Key = "agent_slug"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := hostMetric()
			tc.mutate(&m)
			base := DefaultSpecs()
			got, err := base.Register(m)
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("不自洽登记项应报 ErrInvalidSpec, got %v", err)
			}
			if got.Len() != base.Len() {
				t.Fatalf("失败注册不得改变原表: %d -> %d", base.Len(), got.Len())
			}
		})
	}

	// 重名：登记表内已有同名指标。
	if _, err := DefaultSpecs().Register(Specs()[0]); !errors.Is(err, ErrDuplicateMetric) {
		t.Fatalf("重名应报 ErrDuplicateMetric, got %v", err)
	}
}

// TestSpecSetMerge Merge 保留两侧登记项、拒绝重名且不改写任何一侧。
func TestSpecSetMerge(t *testing.T) {
	left, err := DefaultSpecs().Register(hostMetric())
	if err != nil {
		t.Fatalf("left.Register: %v", err)
	}
	var right SpecSet
	right, err = right.Register(hostSecondMetric())
	if err != nil {
		t.Fatalf("right.Register: %v", err)
	}

	merged, err := left.Merge(right)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for name, labels := range map[string]Labels{
		hostMetric().Name:       {"host.queue.name": "egress"},
		hostSecondMetric().Name: {"host.worker.pool": "pool-b"},
	} {
		if err := merged.Validate(name, labels); err != nil {
			t.Fatalf("合并后 %s 应可校验: %v", name, err)
		}
	}

	// 两侧原表不被改写。
	if _, ok := left.SpecOf(hostSecondMetric().Name); ok {
		t.Fatal("Merge 改写了左侧原表")
	}
	if _, ok := right.SpecOf(hostMetric().Name); ok {
		t.Fatal("Merge 改写了右侧原表")
	}

	// 重名冲突：返回错误且原表不变。
	collide, err := right.Register(hostMetric())
	if err != nil {
		t.Fatalf("构造冲突表: %v", err)
	}
	if _, err := left.Merge(collide); !errors.Is(err, ErrDuplicateMetric) {
		t.Fatalf("合并重名应报 ErrDuplicateMetric, got %v", err)
	}

	// 对侧含不自洽登记项（同包测试可手工构造）同样拒收。
	bad := SpecSet{metrics: []Metric{{Name: "host.bad"}}}
	if _, err := left.Merge(bad); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("合并不自洽登记项应报 ErrInvalidSpec, got %v", err)
	}
}

// TestSpecSetCloneIsolation Clone/副本语义：改副本（含 Labels/Values 子切片）
// 不影响原表，也不影响包级登记表。
func TestSpecSetCloneIsolation(t *testing.T) {
	set := DefaultSpecs()
	first := set.Metrics()[0]
	if first.Name == "" || len(first.Labels) == 0 || len(first.Labels[0].Values) == 0 {
		t.Fatal("前提失效：默认登记表首项应带标签取值")
	}
	name := first.Name
	want := first.Labels[0].Values[0]

	// 改 Metrics() 返回的深拷贝。
	cp := set.Metrics()
	cp[0].Name = "host.mutated"
	cp[0].Labels[0].Values[0] = "mutated-value"
	cp[0].Labels[0].Why = "mutated-why"

	if _, ok := set.SpecOf("host.mutated"); ok {
		t.Fatal("改写 Metrics() 副本影响了原表")
	}
	got, ok := set.SpecOf(name)
	if !ok || got.Labels[0].Values[0] != want || got.Labels[0].Why == "mutated-why" {
		t.Fatalf("改写副本的 Labels/Values 影响了原表: %+v", got)
	}

	// SpecOf 返回的也是深拷贝。
	sp, _ := set.SpecOf(name)
	sp.Labels[0].Values[0] = "mutated-again"
	again, _ := set.SpecOf(name)
	if again.Labels[0].Values[0] != want {
		t.Fatalf("改写 SpecOf 返回值影响了原表: %q", again.Labels[0].Values[0])
	}

	// 包级 Specs() 同样是深拷贝。
	all := Specs()
	all[0].Labels[0].Values[0] = "pkg-mutated"
	if pkg, _ := SpecOf(name); pkg.Labels[0].Values[0] != want {
		t.Fatalf("改写 Specs() 返回值污染了包级登记表: %q", pkg.Labels[0].Values[0])
	}
}
