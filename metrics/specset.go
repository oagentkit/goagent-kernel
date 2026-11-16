// specset.go —— 可扩展的指标登记表（issue #239）。
//
// 包级 specs 是**内核口径**的单一来源：它不能因为宿主注册自有指标而被改写
// （口径只有一处能改）。但「宿主自有指标也要能复用同一套校验」同样是真需求
// ——否则宿主只能照抄一份 Validate / 基数门，清单漂移时两边各自静默。
//
// SpecSet 是这两件事的接缝：**值语义 + 复制语义**，Register / Merge / Clone
// 都返回新表；DefaultSpecs 只以深拷贝的形式外借包级表。于是：
//
//   - 宿主 `DefaultSpecs().Register(自己的指标)` 后，Validate 的未登记名 /
//     未声明标签 / 越界取值 / 禁区键判据对自有指标生效；
//   - 包级 Specs()/SpecOf()/Validate 仍只认 harness.* 的登记表，一字不改。
package metrics

import (
	"fmt"

	"github.com/oekit/pkg/errors"
)

// ErrInvalidSpec 一条指标登记项自身不自洽（名称 / 类型 / 单位 / 说明 /
// 维度取值域 / 基数上界依据缺一不可）。
var ErrInvalidSpec = errors.New("metrics: 指标登记项不自洽")

// ErrDuplicateMetric 同一份登记表里指标名重复。
//
// 看板与查询按名字对应，重名等于两个口径——注册与合并都拒收，不做
// 「后者覆盖前者」的静默替换。
var ErrDuplicateMetric = errors.New("metrics: 指标名重复")

// SpecSet 一份指标登记表。**零值可用**（空表）。
//
// 方法按值操作、返回新表：对返回值做任何改写（含 Labels / Values 子切片）
// 都不会回流到原表。宿主的典型用法：
//
//	set, err := metrics.DefaultSpecs().Register(myMetric)
//	if err != nil { ... }
//	if err := set.Validate(myMetric.Name, labels); err != nil { ... }
type SpecSet struct {
	metrics []Metric
}

// DefaultSpecs 包级缺省登记表的深拷贝。
//
// 「拷贝」是契约的一部分：宿主可以把它当起点注册自有指标，包级表不受影响；
// 反过来，内核后续增补登记项时，先前拿到的副本也不会被悄悄改写。
func DefaultSpecs() SpecSet { return SpecSet{metrics: cloneMetrics(specs)} }

// Clone 深拷贝本表（Labels / Values 子切片同样复制）。
func (s SpecSet) Clone() SpecSet { return SpecSet{metrics: cloneMetrics(s.metrics)} }

// Len 登记项条数。
func (s SpecSet) Len() int { return len(s.metrics) }

// Metrics 返回全部登记项的深拷贝（调用方改不动表内数据）。
func (s SpecSet) Metrics() []Metric { return cloneMetrics(s.metrics) }

// SpecOf 按名取登记项（深拷贝）。未登记 → (Metric{}, false)。
func (s SpecSet) SpecOf(name string) (Metric, bool) {
	m, ok := findSpec(s.metrics, name)
	if !ok {
		return Metric{}, false
	}
	return cloneMetric(m), true
}

// Register 返回注册一项后的新表。登记项不自洽 → ErrInvalidSpec；
// 表内已有同名指标 → ErrDuplicateMetric。失败时原表不受影响。
//
// 登记名不要求 harness. 前缀：宿主自有指标用自己的命名空间；前缀纪律
// 只约束内核登记表（metrics_test 判），不约束宿主。
func (s SpecSet) Register(m Metric) (SpecSet, error) {
	if err := validateMetric(m); err != nil {
		return s, err
	}
	if _, ok := findSpec(s.metrics, m.Name); ok {
		return s, errors.Wrap(ErrDuplicateMetric, fmt.Sprintf("指标 %q 已在登记表内", m.Name))
	}
	out := s.Clone()
	out.metrics = append(out.metrics, cloneMetric(m))
	return out, nil
}

// Merge 返回合并后的新表：逐项注册 other 的登记项（同样校验、同样拒重名）。
// 任一项失败时返回错误与原表（未合并）。
func (s SpecSet) Merge(other SpecSet) (SpecSet, error) {
	out := s.Clone()
	for _, m := range other.metrics {
		next, err := out.Register(m)
		if err != nil {
			return s, err
		}
		out = next
	}
	return out, nil
}

// Validate 在本表内校验一次埋点：禁区键 / 指标名已登记 / 标签键在登记的
// 维度里 / 取值落在封闭枚举内。判据与包级 Validate 同一实现（一个真相）。
func (s SpecSet) Validate(name string, labels Labels) error {
	return validateAgainst(s.metrics, name, labels)
}

// ── 内部共用实现 ────────────────────────────────────────────────────────────

// findSpec 在登记表里按名查登记项（不拷贝；调用方需要外借时自行 clone）。
func findSpec(table []Metric, name string) (Metric, bool) {
	for _, m := range table {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}

// validateAgainst 包级 Validate 与 SpecSet.Validate 共用的判定体。
func validateAgainst(table []Metric, name string, labels Labels) error {
	if err := CheckLabels(labels); err != nil {
		return err
	}
	m, ok := findSpec(table, name)
	if !ok {
		return errors.Wrap(ErrUnregisteredMetric, fmt.Sprintf("埋点 %q", name))
	}
	declared := make(map[string]LabelDim, len(m.Labels))
	for _, d := range m.Labels {
		declared[d.Key] = d
	}
	for k, v := range labels {
		d, ok := declared[k]
		if !ok {
			return fmt.Errorf("指标 %s 未声明标签 %q（登记表是口径的唯一来源）", name, k)
		}
		if !containsValue(d.Values, v) {
			return fmt.Errorf("标签 %s=%q 越界：登记的取值域是 %v", k, v, d.Values)
		}
	}
	return nil
}

// validateMetric 登记项自洽性判据（Register / Merge 入口共用）。
//
// 与 metrics_test 的登记表自校验同口径，但**不含** harness. 前缀要求——
// 那是内核登记表的纪律，不是宿主指标的门槛。
func validateMetric(m Metric) error {
	if m.Name == "" {
		return errors.Wrap(ErrInvalidSpec, "指标缺名字")
	}
	switch m.Kind {
	case KindCounter, KindGauge, KindHistogram:
	default:
		return errors.Wrap(ErrInvalidSpec,
			fmt.Sprintf("指标 %q 类型 %q 非法（counter/gauge/histogram 三选一）", m.Name, m.Kind))
	}
	if m.Unit == "" {
		return errors.Wrap(ErrInvalidSpec,
			fmt.Sprintf("指标 %q 没写单位（计数/仪表写 \"1\"，耗时写 ms）", m.Name))
	}
	if m.Desc == "" {
		return errors.Wrap(ErrInvalidSpec, fmt.Sprintf("指标 %q 没写语义说明", m.Name))
	}
	keys := make(map[string]bool, len(m.Labels))
	for _, d := range m.Labels {
		if d.Key == "" {
			return errors.Wrap(ErrInvalidSpec, fmt.Sprintf("指标 %q 有标签缺键", m.Name))
		}
		if keys[d.Key] {
			return errors.Wrap(ErrInvalidSpec,
				fmt.Sprintf("指标 %q 的标签键 %q 重复", m.Name, d.Key))
		}
		keys[d.Key] = true
		if len(d.Values) == 0 {
			return errors.Wrap(ErrInvalidSpec,
				fmt.Sprintf("指标 %q 的标签 %q 没有取值域（无界基数就是这里漏进来的）", m.Name, d.Key))
		}
		if d.Why == "" {
			return errors.Wrap(ErrInvalidSpec,
				fmt.Sprintf("指标 %q 的标签 %q 基数上界没写依据", m.Name, d.Key))
		}
		for _, f := range ForbiddenLabels {
			if f.Name == d.Key {
				return errors.Wrap(ErrInvalidSpec,
					fmt.Sprintf("指标 %q 的标签 %q 在禁区清单里（%s）", m.Name, d.Key, f.Why))
			}
		}
		vals := make(map[string]bool, len(d.Values))
		for _, v := range d.Values {
			if vals[v] {
				return errors.Wrap(ErrInvalidSpec,
					fmt.Sprintf("指标 %q 的标签 %q 取值 %q 重复", m.Name, d.Key, v))
			}
			vals[v] = true
		}
	}
	return nil
}

// containsValue 判 v 是否落在有界取值域内。
func containsValue(values []string, v string) bool {
	for _, got := range values {
		if got == v {
			return true
		}
	}
	return false
}

// cloneMetric 登记项深拷贝（Labels 与每个 LabelDim.Values）。
func cloneMetric(m Metric) Metric {
	out := m
	if m.Labels == nil {
		return out
	}
	out.Labels = make([]LabelDim, len(m.Labels))
	for i, d := range m.Labels {
		out.Labels[i] = d
		if d.Values != nil {
			out.Labels[i].Values = append([]string(nil), d.Values...)
		}
	}
	return out
}

// cloneMetrics 登记表深拷贝。
func cloneMetrics(in []Metric) []Metric {
	if in == nil {
		return nil
	}
	out := make([]Metric, len(in))
	for i, m := range in {
		out[i] = cloneMetric(m)
	}
	return out
}
