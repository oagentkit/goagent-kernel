package metrics

// align_test.go —— 跨包标签维度的对齐用例（issue #395）。
//
// 本包的标签取值域有的是**别的包的词表**（轨迹的行类目与状态、重试路径、租约
// 拒绝形态）。抄一份就是两份实现——两边各自长大时看板在某个无声的时刻拼不
// 起来，而且**不会报错**。已有的两条比对用例（类目 / 重试路径）钉住了同类目；
// 这一族补齐剩下的维度，并加一条「承诺了同源就必须有比对用例」的判据。

import (
	"slices"
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/trajectory"
)

// TestExecStatusAlignsWithTrajectorySpanStatus 执行状态三值与 trajectory 的
// span 状态三值逐字对齐（与类目维度同形的一条判据）。
//
// 反向验证：给 trajectory 加一个 SpanStatus ⇒ 本用例红；
// 反向验证 2：把 metrics.ExecStatusError 改成 "failure" ⇒ 本用例红。
func TestExecStatusAlignsWithTrajectorySpanStatus(t *testing.T) {
	want := trajectory.SpanStatusValues()
	dim := labelDimOf(t, NameExecDuration, LabelExecStatus)
	if dim.Card() != len(want) {
		t.Fatalf("状态维度基数 %d 与 trajectory.SpanStatus 取值数 %d 不一致", dim.Card(), len(want))
	}
	for _, w := range want {
		if !slices.Contains(dim.Values, w) {
			t.Errorf("trajectory span 状态 %q 不在耗时指标的状态维度里：trajectory 加了状态、metrics 没跟上", w)
		}
	}
	for _, v := range dim.Values {
		if !slices.Contains(want, v) {
			t.Errorf("指标状态维度里的 %q 不在 trajectory 的 span 状态词表里：两边各写一份就会漂", v)
		}
	}
}

// labelDimOf 取登记项里某个标签维度的取值域（找不到即失败——判据不许空转）。
func labelDimOf(t *testing.T, metric, key string) LabelDim {
	t.Helper()
	spec, ok := SpecOf(metric)
	if !ok {
		t.Fatalf("指标 %s 未登记", metric)
	}
	for _, d := range spec.Labels {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("指标 %s 没有标签维度 %s", metric, key)
	return LabelDim{}
}

// TestLeaseOutcomeAlignsWithSideeffectOutcome 租约拒绝形态与 sideeffect.Outcome
// 的两个拒绝态逐字对齐。
//
// sideeffect.Outcome 是 int 枚举 ⇒ 比对的是 String() 而非常量（常量不可比）。
func TestLeaseOutcomeAlignsWithSideeffectOutcome(t *testing.T) {
	want := []string{
		sideeffect.OutcomeContended.String(),
		sideeffect.OutcomeUncertain.String(),
	}
	dim := labelDimOf(t, NameLeaseContended, LabelLeaseOutcome)
	if dim.Card() != len(want) {
		t.Fatalf("租约形态维度基数 %d 与 sideeffect 拒绝态数 %d 不一致", dim.Card(), len(want))
	}
	for _, w := range want {
		if !slices.Contains(dim.Values, w) {
			t.Errorf("sideeffect 拒绝形态 %q 不在租约指标的形态维度里：sideeffect 加了形态、metrics 没跟上", w)
		}
	}
}

// crossPkgAlignments 跨包对齐用例登记表：标签维度 → 比对用例。
//
// 这张表是「依据里承诺了跨包同源」与「谁在比对它」之间的映射：下面那条判据
// 判「承诺了的必须有比对用例」，反向验证（删掉比对用例）因此必红。
var crossPkgAlignments = map[string]func(*testing.T){
	LabelExecKind:     TestExecKindAlignsWithTrajectory,
	LabelExecStatus:   TestExecStatusAlignsWithTrajectorySpanStatus,
	LabelRetryPath:    TestRetryPathAlignsWithRetryPlan,
	LabelLeaseOutcome: TestLeaseOutcomeAlignsWithSideeffectOutcome,
}

// citesCrossPackage 依据（Why）是否承诺了「与另一个包同源」。
func citesCrossPackage(why string) bool {
	for _, kw := range []string{"同源", "逐字对齐", "两边同步"} {
		if strings.Contains(why, kw) {
			return true
		}
	}
	return false
}

// TestLabelDimsWithCrossPackageCitationHaveComparison 凡依据里写了「同源 /
// 逐字对齐 / 须两边同步」的标签维度，都必须有对应的比对用例。
//
// 反向验证：删掉新加的比对用例（连同登记表项一起删）⇒ 本用例红。
func TestLabelDimsWithCrossPackageCitationHaveComparison(t *testing.T) {
	dims := map[string]LabelDim{}
	for _, mm := range Specs() {
		for _, d := range mm.Labels {
			dims[d.Key] = d
		}
	}
	for key, d := range dims {
		if !citesCrossPackage(d.Why) {
			continue
		}
		if _, ok := crossPkgAlignments[key]; !ok {
			t.Errorf("标签 %s 的依据承诺了跨包同源（%q），却没有对应的比对用例：两份枚举各自长大时看板就拼不起来了",
				key, d.Why)
		}
	}
	for key := range crossPkgAlignments {
		if _, ok := dims[key]; !ok {
			t.Errorf("对齐登记表登记了 %s，但登记表里没有这个维度（判据空转）", key)
		}
	}
}
