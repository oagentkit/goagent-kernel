package metrics

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

// 本文件是开放登记面（issue #239）的判据：
//   - 宿主自有指标装配期 Register 后，SpecOf / Specs / Validate 复用同一份
//     名称/维度/基数门（不必各抄第二份真相）；
//   - Register 的 fail-closed 判据（空字段 / Kind 词表 / 禁区维度 /
//     空取值域 / 撞名 / harness.* 前缀保留）；
//   - 内核表不受注册污染（TestSpecsSelfCheck 仍钉 harness.* 口径）。

func hostMetric(name string) Metric {
	return Metric{
		Name: name,
		Kind: KindCounter,
		Unit: "1",
		Desc: "宿主自有指标（回归用）：意图分档计数。",
		Labels: []LabelDim{
			{Key: "host_intent", Values: []string{"book", "chitchat"}, Why: "意图词表宿主写死，两值封闭"},
		},
	}
}

// Test宿主指标注册后复用判据门 —— issue #239 验收原句：宿主自有指标
// 「可复用 Validate/基数门」。注册前 Validate 拒（未登记），注册后同一份
// 判据放行合法取值、拦越界取值；SpecOf / Specs 同步可见。
func Test宿主指标注册后复用判据门(t *testing.T) {
	name := "host_intents_total_test1"
	if err := Validate(name, Labels{"host_intent": "book"}); err == nil {
		t.Fatal("未注册的宿主指标必须被 Validate 拦（埋点面之外的暗数）")
	}
	if err := Register(hostMetric(name)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := SpecOf(name); !ok {
		t.Fatal("注册后 SpecOf 必须可见")
	}
	if !slices.ContainsFunc(Specs(), func(m Metric) bool { return m.Name == name }) {
		t.Fatal("注册后 Specs 必须可见")
	}
	// 合法取值放行；越界取值与未声明键仍被同一份门拦住。
	if err := Validate(name, Labels{"host_intent": "book"}); err != nil {
		t.Fatalf("注册后合法埋点应放行: %v", err)
	}
	if err := Validate(name, Labels{"host_intent": "sing"}); err == nil {
		t.Fatal("越界取值必须被拦（封闭枚举门对宿主指标同样生效）")
	}
	if err := Validate(name, Labels{"host_mood": "happy"}); err == nil {
		t.Fatal("未声明维度必须被拦")
	}
	// 禁区键对宿主指标同援同一份 ForbiddenLabels（CheckLabels 不变）。
	if err := Validate(name, Labels{"host_intent": "book", "run_id": "r1"}); err == nil {
		t.Fatal("禁区键必须被拦")
	}
	// Mem.Strict 语义对宿主指标一致：违规计数、合法不计。
	mem := &Mem{Strict: true}
	mem.Count(name, 1, Labels{"host_intent": "sing"})
	mem.Count(name, 1, Labels{"host_intent": "book"})
	if mem.Bad != 1 {
		t.Fatalf("Strict 模式应记 1 次违规，实得 %d", mem.Bad)
	}
}

// TestRegister判据failclosed —— 登记面自身的基数门：声明自相矛盾的登记
// 一律拒绝（与 TestSpecsSelfCheck 对内核表的判据同源）。
func TestRegister判据failclosed(t *testing.T) {
	cases := []struct {
		name   string
		m      Metric
		substr string // 报错必须点名的关键词
	}{
		{"缺指标名", Metric{Kind: KindCounter, Unit: "1", Desc: "d"}, ""},
		{"harness前缀保留", Metric{Name: "harness.sneaky", Kind: KindCounter, Unit: "1", Desc: "d"}, "保留"},
		{"缺语义说明", Metric{Name: "host_no_desc", Kind: KindCounter, Unit: "1"}, "Desc"},
		{"Kind词表外", Metric{Name: "host_bad_kind", Kind: Kind("summary"), Unit: "1", Desc: "d"}, "Kind"},
		{"缺单位", Metric{Name: "host_no_unit", Kind: KindCounter, Desc: "d"}, "Unit"},
		{"禁区维度", Metric{Name: "host_forbidden_label", Kind: KindCounter, Unit: "1", Desc: "d",
			Labels: []LabelDim{{Key: "run_id", Values: []string{"a"}, Why: "w"}}}, "禁区"},
		{"空取值域", Metric{Name: "host_empty_domain", Kind: KindCounter, Unit: "1", Desc: "d",
			Labels: []LabelDim{{Key: "host_x", Values: nil, Why: "w"}}}, "取值域为空"},
		{"取值域重复", Metric{Name: "host_dup_domain", Kind: KindCounter, Unit: "1", Desc: "d",
			Labels: []LabelDim{{Key: "host_x", Values: []string{"a", "a"}, Why: "w"}}}, "重复"},
		{"维度键重复", Metric{Name: "host_dup_key", Kind: KindCounter, Unit: "1", Desc: "d",
			Labels: []LabelDim{
				{Key: "host_a", Values: []string{"a"}, Why: "w"},
				{Key: "host_a", Values: []string{"b"}, Why: "w"},
			}}, "维度键重复"},
		{"撞内核口径（前缀规则先行）", func() Metric {
			m := hostMetric("host_kernel_collision")
			m.Name = NameRetryTotal
			return m
		}(), "保留"},
	}
	for _, tc := range cases {
		err := Register(tc.m)
		if err == nil {
			t.Fatalf("%s: 必须拒绝登记", tc.name)
		}
		if !strings.Contains(err.Error(), tc.substr) {
			t.Fatalf("%s: 报错应点名 %q，实得 %q", tc.name, tc.substr, err.Error())
		}
	}
	// 与已注册宿主指标撞名：静默覆盖会让口径分叉没有信号。
	if err := Register(hostMetric("host_intents_total_test1")); err == nil {
		t.Fatal("重复注册必须报错（上一用例已注册 host_intents_total_test1）")
	}
}

// Test内核表不受注册污染 —— 开放登记面不得改变内核口径的可机器校验性：
// 内核表（kernelSpecs）恒定，注册只进宿主表；harness.* 总数不随注册膨胀。
func Test内核表不受注册污染(t *testing.T) {
	before := len(kernelSpecs)
	if err := Register(hostMetric("host_pollution_probe")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(kernelSpecs) != before {
		t.Fatalf("注册进了内核表（%d → %d）：内核口径被宿主污染", before, len(kernelSpecs))
	}
	if !slices.ContainsFunc(Specs(), func(m Metric) bool { return m.Name == "host_pollution_probe" }) {
		t.Fatal("宿主表应进 Specs 的并集")
	}
}

// TestRegister并发安全 —— 装配语义的并发口径：并发注册 + 并发读
// （SpecOf/Specs/Validate）不数据竞争、不丢登记（-race 钉）。
func TestRegister并发安全(t *testing.T) {
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = Register(hostMetric("host_concurrent_probe")) // 只有一个赢，其余撞名报错
			_, _ = SpecOf("host_pollution_probe")
			_ = Specs()
			_ = Validate("host_pollution_probe", Labels{"host_intent": "book"})
		}(i)
	}
	wg.Wait()
	if _, ok := SpecOf("host_concurrent_probe"); !ok {
		t.Fatal("并发注册后恰好一份必须可见")
	}
}
