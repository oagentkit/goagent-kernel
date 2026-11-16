// registry.go —— 指标登记表：内核口径（kernelSpecs）+ 宿主开放登记面
// （Register，issue #239）。名称/维度/基数上界的**单一来源**在这里；
// Validate / SpecOf / Specs 消费它，宿主自有指标装配期注册后走同一份门。
//
// 两张表的分工：kernelSpecs 是 harness.* 内核口径（包内写死，自校验
// metrics_test 对它跑全量不变量）；hostSpecs 只经 [Register] 增长
// （判据 fail-closed，见其注释），harness.* 前缀保留给内核——看板按
// 前缀分得清口径来源。
package metrics

import (
	"fmt"
	"strings"
	"sync"

	"github.com/oekit/pkg/errors"
)

// kernelSpecs 内核登记表（harness.* 口径）。**先登记再埋点**：内核新增指标
// 在这里加一行 + ACCESS-POINTS.md 加一个接入点，自校验（metrics_test 对本表
// 跑全量不变量）才认。宿主指标不进本表——宿主注册进 hostSpecs（见
// [Register]），harness.* 命名空间保留给内核，看板才分得清口径来源。
var kernelSpecs = []Metric{
	{
		Name: NameExecDuration,
		Kind: KindHistogram,
		Unit: "ms",
		Desc: "一次编排单元（轮 / 环节 / 工具调用 / 副作用 / 快照落库）的实测耗时。" +
			"口径与轨迹属性 " + "harness.trajectory.duration_ms" + " 同源：起止相减现算，" +
			"只记实测，估算值不许冒充实测。",
		Labels: []LabelDim{
			{
				Key: LabelExecKind,
				Values: []string{
					ExecKindTurn, ExecKindStep, ExecKindToolCall,
					ExecKindToolResult, ExecKindSideEffect, ExecKindCheckpoint,
				},
				Why: "逐字对齐 trajectory 的行类目（Kind* 常量），轨迹上加类目才加这里——两份枚举不许各自长",
			},
			{
				Key:    LabelExecStatus,
				Values: []string{ExecStatusUnset, ExecStatusOK, ExecStatusError},
				Why:    "状态三值纪律与 trajectory 同源：在途记 unset 不记 error，事故不被在途噪声淹没",
			},
		},
	},
	{
		Name: NameRetryTotal,
		Kind: KindCounter,
		Unit: "1",
		Desc: "重试决策计数。每次 retryplan.Decide 落地一条（按 path 分）——数的是" +
			"「决定走哪条路」，不是「实际重发了几次」：后者是宿主执行面的事，内核不猜。",
		Labels: []LabelDim{
			{
				Key:    LabelRetryPath,
				Values: []string{RetryPathNone, RetryPathDirect, RetryPathReplayed},
				Why:    "取值 = retryplan.Path 的 String()，路径枚举加值须两边同步（metrics_test 比对）",
			},
		},
	},
	{
		Name: NameLeaseContended,
		Kind: KindCounter,
		Unit: "1",
		Desc: "租约竞争拒绝计数：「这次没执行成」的竞争面信号。contended=等一等再来，" +
			"uncertain=须先对账（硬阻塞）。两类合看就是「竞争压力」与「对账压力」。",
		Labels: []LabelDim{
			{
				Key:    LabelLeaseSubsystem,
				Values: []string{LeaseSubsystemSideEffect, LeaseSubsystemTaskBoard},
				Why:    "租约面只有 sideeffect（副作用闸）与 taskboard（任务认领）两个子系统；加新租约面才算新值",
			},
			{
				Key:    LabelLeaseOutcome,
				Values: []string{LeaseOutcomeContended, LeaseOutcomeUncertain},
				Why:    "拒绝形态对齐 sideeffect.Outcome 的 Contended / Uncertain 二值",
			},
		},
	},
	{
		Name: NameRecoveryTotal,
		Kind: KindCounter,
		Unit: "1",
		Desc: "恢复尝试计数（按 outcome 分）。恢复成功率由看板用 success / failure 两时序" +
			"现算——内核不产出第二个真相（派生量只算一遍）。",
		Labels: []LabelDim{
			{
				Key:    LabelRecoverySubsystem,
				Values: []string{RecoverySubsystemCheckpoint, RecoverySubsystemSideEffect},
				Why:    "恢复面目前只有快照恢复与副作用对账两处；新增恢复路径才加值",
			},
			{
				Key:    LabelRecoveryOutcome,
				Values: []string{RecoveryOutcomeSuccess, RecoveryOutcomeFailure},
				Why:    "两值封闭：blocked 也计 failure——「没恢复成」只有一种计法，看板不用先学会内核细分语义",
			},
		},
	},
	{
		Name: NameUncertainBacklog,
		Kind: KindGauge,
		Unit: "1",
		Desc: "未知结果积压量：当前处于「结果未知」态的副作用 Key 数。" +
			"零维（无标签）——积压看总量与趋势，明细（哪个 Key）走日志与对账面，不进指标。",
	},
}

// specsMu 保护登记表（kernelSpecs 只读 + hostSpecs 注册写）的并发。
// 宿主在装配期 Register、埋点/校验路径随时读——并发安全由这里统一给
// （SetDefault 同款装配语义）。
var (
	specsMu sync.RWMutex
	// hostSpecs 宿主经 [Register] 注册的自有指标（issue #239 开放登记面）。
	hostSpecs []Metric
)

// Specs 返回登记表的副本：内核 harness.* 口径 + 宿主经 [Register] 注册的
// 自有指标（调用方改不动登记表——口径只有一处能改）。
func Specs() []Metric {
	specsMu.RLock()
	defer specsMu.RUnlock()
	out := make([]Metric, 0, len(kernelSpecs)+len(hostSpecs))
	out = append(out, kernelSpecs...)
	out = append(out, hostSpecs...)
	return out
}

// SpecOf 按名取登记项（内核表与宿主开放表同查）。未登记的指标名 =
// 埋点面之外的暗数，Validate 会拦。
func SpecOf(name string) (Metric, bool) {
	specsMu.RLock()
	defer specsMu.RUnlock()
	for _, m := range kernelSpecs {
		if m.Name == name {
			return m, true
		}
	}
	for _, m := range hostSpecs {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}

// ErrInvalidSpec 指标登记项不合法（[Register] 的判据拒绝）。
var ErrInvalidSpec = errors.New("metrics: 指标登记项不合法")

// Register 登记一个指标到**开放的**宿主登记面（issue #239）：宿主自有指标
// 装配期注册后即可复用本包全部判据面——SpecOf / Specs / Validate /
// Mem.Strict 对宿主指标与 harness.* 一视同仁（同一份名称/维度/基数门，
// 不必各抄一份第二真相）。重复注册同一指标报错收口（静默覆盖会让
// 「两处各登记一份、口径已分叉」没有信号）。
//
// 判据（fail-closed，违者 ErrInvalidSpec 包装上抛）：
//
//   - Name / Kind / Unit / Desc 非空，Kind 在三类词表内——「数的是什么」
//     写不出来 = 没想清楚，不许登记；
//   - Name **不得带 harness. 前缀**（内核命名空间保留；看板按前缀分得清
//     口径来源），且与登记表任一项（内核 + 已注册宿主指标）不重复；
//   - 维度键非空、不在 [ForbiddenLabels] 禁区清单里、同一指标内不重复；
//   - 维度取值域 Values 非空且无重复——空域/含重复的「封闭枚举」是
//     声明自相矛盾，基数门会失去可信上界。
//
// 装配期调用一次；运行期注册是宿主自己的并发决定，本包保证调用安全
// （读写锁），但登记表在中途膨胀会让「看板见到哪些指标」随时刻漂移。
func Register(m Metric) error {
	if m.Name == "" {
		return errors.Wrap(ErrInvalidSpec, "缺指标名")
	}
	if strings.HasPrefix(m.Name, "harness.") {
		return errors.Wrap(ErrInvalidSpec, "harness. 前缀保留给内核口径（宿主指标不得冒用）："+m.Name)
	}
	if m.Desc == "" {
		return errors.Wrap(ErrInvalidSpec, "缺语义说明（Desc）："+m.Name)
	}
	switch m.Kind {
	case KindCounter, KindGauge, KindHistogram:
	default:
		return errors.Wrap(ErrInvalidSpec, "Kind 不在词表内（counter/gauge/histogram）："+m.Name)
	}
	if m.Unit == "" {
		return errors.Wrap(ErrInvalidSpec, "缺单位（Unit）："+m.Name)
	}
	seenKeys := make(map[string]bool, len(m.Labels))
	for _, d := range m.Labels {
		if d.Key == "" {
			return errors.Wrap(ErrInvalidSpec, "维度键为空："+m.Name)
		}
		if seenKeys[d.Key] {
			return errors.Wrap(ErrInvalidSpec, "维度键重复："+m.Name+" / "+d.Key)
		}
		seenKeys[d.Key] = true
		for _, f := range ForbiddenLabels {
			if f.Name == d.Key {
				return errors.Wrap(ErrInvalidSpec,
					fmt.Sprintf("维度 %q 在禁区清单里（%s）：%s", d.Key, f.Why, m.Name))
			}
		}
		if len(d.Values) == 0 {
			return errors.Wrap(ErrInvalidSpec, "维度取值域为空（封闭枚举必须至少一个值）："+m.Name+" / "+d.Key)
		}
		seenVals := make(map[string]bool, len(d.Values))
		for _, v := range d.Values {
			if seenVals[v] {
				return errors.Wrap(ErrInvalidSpec, "维度取值域含重复值："+m.Name+" / "+d.Key+" / "+v)
			}
			seenVals[v] = true
		}
	}

	specsMu.Lock()
	defer specsMu.Unlock()
	for _, existing := range kernelSpecs {
		if existing.Name == m.Name {
			return errors.Wrap(ErrInvalidSpec, "指标名与内核口径撞名："+m.Name)
		}
	}
	for _, existing := range hostSpecs {
		if existing.Name == m.Name {
			return errors.Wrap(ErrInvalidSpec, "指标名已注册（静默覆盖会让口径分叉没有信号）："+m.Name)
		}
	}
	hostSpecs = append(hostSpecs, m)
	return nil
}
