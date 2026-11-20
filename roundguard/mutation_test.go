package roundguard_test

import "testing"

// ── 反向验证矩阵：判据 × 变异 × 用例 ──
//
// 本文件是 roundguard 的**反向验证矩阵**：一张表，把「哪条判据」「把它改坏会是
// 什么样（变异）」「哪条用例钉住它」摆在一起。
//
// 为什么要有这张表：本机制**全行业空白**（五家主流实现零命中，见 spec §1.4）⇒
// 没有可抄的对照实现 ⇒ **「坏实现会红」是本包唯一的正确性来源**。而「坏实现会
// 红」零散钉在各用例的注释里时，人读不到**全貌**：哪条判据有对照、哪条没有、
// 哪条对照其实是「好实现能过」而不是「坏实现会红」。
//
// 这张表的形状：
//
//	机制分组 → 判据 → 变异类 → 钉住它的用例（Test 函数）
//
// 它做三件事：① 汇总全貌；② 把「每条用例钉住哪个变异」**写死**（写下来才算数）；
// ③ 棘轮：四个机制分组一个都不能少（新增机制却忘了钉反向验证 ⇒ 当天即红）。
//
// 另一半在**内部测试包**（mutation_internal_test.go）：承重守卫只在未导出面上，
// Go 的包边界决定它只能由内部包直钉。两半合起来才是完整的矩阵。
//
// 合成数据：夹具只用中性词面（指纹用字母串、工具标识用 read / write）。

// matrixRow 反向验证矩阵的一行。
type matrixRow struct {
	mechanism string           // 机制分组
	criterion string           // 判据（一句）
	mutant    string           // 变异类（把它改坏是什么样）
	pin       func(*testing.T) // 钉住它的用例
}

// mxRow 造一行矩阵（少写一个字段就是少一条对照 ⇒ 构造函数强制四个字段齐）。
func mxRow(mechanism, criterion, mutant string, pin func(*testing.T)) matrixRow {
	return matrixRow{mechanism: mechanism, criterion: criterion, mutant: mutant, pin: pin}
}

// mutationMatrix 反向验证矩阵（外部包这一半；白盒那一半在内部包）。
var mutationMatrix = []matrixRow{
	// ── 结局分类 ──
	mxRow("结局分类", "输入空间全穷举：8 个臂各自可达 + 判定次序（128 种组合）",
		"把任意两个臂对调 / 删掉一个臂 ⇒ 该类输入的结局变了",
		TestMutationMatrixClassifyExhaustive),
	mxRow("结局分类", "Continue 只在「要工具 / 原样再问」为真",
		"把 Continue 恒为 true ⇒ 终局被判成可继续（宿主据此再转一圈）",
		TestMutationMatrixClassifyContinueOnlyOnContinuable),
	mxRow("结局分类", "「要工具」优先于「给终答」",
		"把两个臂对调 ⇒ 一边收尾一边要工具时判成已完成（谎报已完成）",
		TestClassifyToolCallsBeatsCompleted),
	mxRow("结局分类", "「强制收尾」优先于「普通终答」",
		"把两个臂对调 ⇒ 额度耗尽后的收尾步被判成普通终答",
		TestClassifyForcedFinalBeatsCompleted),
	mxRow("结局分类", "失败型与温和型可区分（IsFailure）",
		"让 IsFailure 对全部取值返回 true ⇒ 温和叫停被当故障上报（假红）",
		TestIsFailureSeparatesGracefulFromFailure),
	mxRow("结局分类", "零值形态 = 保守收敛（不冒充任何结局）",
		"让零值冒充其它结局 ⇒ 空循环被当成有产出",
		TestClassifyZeroOutcomeIsConservative),

	// ── 次数预算 ──
	mxRow("次数预算", "回合数额度边界（上限 3 / 已用 2 放行、已用 3 拒绝）",
		"把 >= 写成 > ⇒ 少用一轮仍被拒",
		TestCheckRoundsBoundary),
	mxRow("次数预算", "工具调用总额度边界",
		"把 >= 写成 > ⇒ 少用一次仍被拒",
		TestCheckToolCallsBoundary),
	mxRow("次数预算", "额度判定的全网格（上限 × 已用 × 工具）",
		"把 < 写成 <= / 去掉工具维度过滤 ⇒ 边界格判错",
		TestMutationMatrixBudgetCheckGrid),
	mxRow("次数预算", "零值 = 显式关闭该额度（三个维度一致）",
		"把零值当成上限 0 ⇒ 立刻叫停（fail-closed 变 fail-open 的反面）",
		TestMutationMatrixBudgetZeroMeansUnlimited),
	mxRow("次数预算", "单工具上限只作用于该工具（Detail 带工具标识）",
		"去掉工具维度过滤 ⇒ 别的工具被误拒",
		TestCheckToolIsPerToolScoped),
	mxRow("次数预算", "单工具上限 0 = 该工具不限",
		"把 0 当成上限 0 ⇒ 该工具立刻被拒",
		TestCheckToolZeroLimitMeansUnlimited),
	mxRow("次数预算", "未登记工具不受任何约束（额度表是开放键）",
		"把额度表当白名单 ⇒ 未登记工具全被拒",
		TestCheckToolUnregisteredToolIsUnlimited),
	mxRow("次数预算", "5 条非法预算路径逐条带哨兵 + 点名字段",
		"某条路径改用 errors.New ⇒ 宿主对那一条路径静默失去哨兵",
		TestValidateRejectsIllegalBudgetsWithSentinel),
	mxRow("次数预算", "多键非法时报字典序第一个键（确定性）",
		"改成 map 随机序 ⇒ 同一份声明两次报出不同的键",
		TestValidateReportsDeterministicKey),
	mxRow("次数预算", "处置口径零值合法（缺省 ≠ 非法）",
		"把零值口径判成非法 ⇒ 合法缺省在装配期被拒",
		TestValidateAcceptsZeroPolicy),
	mxRow("次数预算", "词表内两种处置口径都合法",
		"把口径判据写成只认一种 ⇒ 另一种口径装配期报错",
		TestValidateAcceptsKnownPolicies),
	mxRow("次数预算", "额度两维的次序：先回合数、后工具调用总数",
		"把两维次序对调 ⇒ 两维同时撞上时报错维度",
		TestDecidePriorityRoundsBeforeToolCalls),
	mxRow("次数预算", "两维次序在组合结论上的落点（单维各报各的）",
		"把两维合并成一维 ⇒ 单维命中时报错类别",
		TestMutationMatrixDecideBudgetDimensionOrder),

	// ── 无进展检测 ──
	mxRow("无进展检测", "整个输入空间全穷举（20 种回合 × 长度 0..4 × 3 组参数）",
		"改任一条尾部判据 / 调任一次序 / 动窗口钳位 / 改 RC 口径 ⇒ 必有序列分歧",
		TestMutationMatrixDetectExhaustive),
	mxRow("无进展检测", "每个形态的命中阈值恰为 k（震荡 2k）",
		"把阈值放宽成 k-1 ⇒ 差一轮也命中（形态提前触发）",
		TestMutationMatrixDetectThresholdIsK),
	mxRow("无进展检测", "五形态各自可达（两档逐形态）",
		"某一形态整体判不出 ⇒ 该形态的行红",
		TestDetectTiersPerShape),
	mxRow("无进展检测", "判定次序：两个形态同时成立时取更具体的那条",
		"把任意两条形态对调 ⇒ 该对红",
		TestMutationMatrixDetectShapeOrderMatrix),
	mxRow("无进展检测", "重复形态：同指纹连着来",
		"去掉「全等」判定 ⇒ 形态判不出",
		TestDetectRepeat),
	mxRow("无进展检测", "震荡形态：周期 2 交替（周期 1 不算）",
		"把周期 2 判成周期 1 ⇒ A A A 被判成震荡",
		TestDetectOscillation),
	mxRow("无进展检测", "漂移形态：归一后同、原指纹不同",
		"用 Fingerprint 代替 Normalized 比较 ⇒ 形态判不出",
		TestDetectDrift),
	mxRow("无进展检测", "错误螺旋：连着失败",
		"去掉「失败」条件 ⇒ 形态判不出",
		TestDetectErrorSpiralBeatsRepeat),
	mxRow("无进展检测", "空转：零工具且未收敛（已收敛的不算）",
		"去掉 Settled 条件 ⇒ 正常回合被误判空转",
		TestDetectIdleNotTriggeredBySettled),
	mxRow("无进展检测", "豁免面对全部计数生效（重复 / 空转 / 失败螺旋）",
		"让豁免只对重复生效 ⇒ 另两处的计数被污染",
		TestDetectExemptBreaksIdleCount),
	mxRow("无进展检测", "窗口下限向上钳位（停止档必须可达）",
		"去掉钳位 ⇒ 震荡停止档永不可达（判据不可达 = 判据不存在）",
		TestDetectWindowIsClampedUp),
	mxRow("无进展检测", "窗口上界截断（只看最近 Window 个回合）",
		"去掉裁剪 ⇒ 连续段起点算错",
		TestDetectWindowTruncatesStreak),
	mxRow("无进展检测", "「结果变了」打断重复的连续性",
		"去掉 RC 打断 ⇒ 同指纹但结果变了被判成重复",
		TestDetectResultChangedBreaksStreak),
	mxRow("无进展检测", "「结果变了」打断震荡的连续性（全部非末轮位置逐个钉）",
		"去掉任一位置的 RC 打断 ⇒ 该位置被误判震荡",
		TestDetectOscillationBreaksOnEarlierResultChanged),
	mxRow("无进展检测", "「结果变了」之后的轮次重新起算",
		"让后续轮次接着数 ⇒ 连续段起点错一轮",
		TestMutationMatrixDetectOscillationRestartsAfterResultChanged),
	mxRow("无进展检测", "「结果变了」打断漂移的连续性（与重复同口径）",
		"去掉末轮 RC 分支 ⇒ 假阳（序列明明显示了进展）",
		TestDetectDriftBreaksOnLastResultChanged),
	mxRow("无进展检测", "错误螺旋不因「结果变了」打断连续性",
		"让失败螺旋尊重 RC ⇒ 真实死循环被当成进展放过",
		TestMutationMatrixDetectErrorSpiralIgnoresResultChanged),
	mxRow("无进展检测", "空转不因「结果变了」打断连续性",
		"让空转尊重 RC ⇒ 每轮换个说法即可绕过空转判据",
		TestMutationMatrixDetectIdleIgnoresResultChanged),
	mxRow("无进展检测", "Since 指向连续段起点（全局 1 起）",
		"把 +1 写成不加 ⇒ 起点差一轮",
		TestDetectSincePointsAtStreakStart),
	mxRow("无进展检测", "零值配置 = 显式缺省",
		"零值不再等于缺省 ⇒ 宿主不传参数时结论变了",
		TestDetectZeroConfigEqualsDefaults),

	// ── 组合结论 ──
	mxRow("组合结论", "结局终局优先于额度",
		"把额度判定提前 ⇒ 已完成的回合被改判成预算耗尽",
		TestDecidePriorityOutcomeBeatsBudget),
	mxRow("组合结论", "结局终局优先于无进展",
		"把无进展判定提前 ⇒ 已终答的回合被改判成停滞",
		TestDecidePriorityOutcomeBeatsStall),
	mxRow("组合结论", "额度优先于无进展",
		"把无进展判定提前 ⇒ 报出的额度维度错了",
		TestDecidePriorityBudgetBeatsStall),
	mxRow("组合结论", "处置口径分流（两种口径不同结论 + 失败型相反）",
		"让两种口径走同一条分支 ⇒ 处置口径名存实亡",
		TestDecidePolicySplitOnSameBudget),
	mxRow("组合结论", "无进展首档只警告回灌、仍继续",
		"把首档判成停止档 ⇒ 模型没有自我修正的机会",
		TestDecideFirstTierWarnsAndContinues),
	mxRow("组合结论", "无进展停止档温和叫停（不是失败型）",
		"把停止档判成失败型 ⇒ 温和叫停被当故障上报",
		TestDecideStopTierStops),
	mxRow("组合结论", "失败型 / 温和型终局不被额度或无进展改写",
		"把终局当成可继续 ⇒ 结论被后两段改写",
		TestDecideFailureTypeOutcomesAreTerminal),
	mxRow("组合结论", "全组合面全穷举（240 种组合）",
		"把任一段判定提前 / 改分流 ⇒ 必有组合分歧",
		TestMutationMatrixDecideExhaustive),
	mxRow("组合结论", "组合结论是纯函数（两次判定逐字相同）",
		"让结论依赖调用顺序 ⇒ 同一输入两次结论不同",
		TestDecideIsDeterministic),
	mxRow("组合结论", "缺省处置口径 = 强制收尾（零值是合法缺省）",
		"把缺省解析成失败即止 ⇒ 未设口径的宿主口径漂移",
		TestDecideZeroPolicyDefaultsToForceFinal),
}

// TestMutationMatrix 跑矩阵的每一行（每行一个子测试 ⇒ 红了看得出是哪条判据）。
func TestMutationMatrix(t *testing.T) {
	if len(mutationMatrix) == 0 {
		t.Fatal("矩阵为空：没有判据被钉")
	}
	for _, row := range mutationMatrix {
		t.Run(row.mechanism+"/"+row.criterion, row.pin)
	}
}

// TestMutationMatrixCoversEveryMechanism 棘轮：四个机制分组一个都不能少，且
// 每行都得写清「钉住哪个变异」——只写判据不写变异的那行等于没钉。
//
// 为什么是棘轮而不是清单核对：本包每加一条判据就多一种「没被钉住」的可能 ⇒
// 判据表与矩阵表两张表会漂 ⇒ 漂了没人知道。这条用例让漂**当天即红**。
func TestMutationMatrixCoversEveryMechanism(t *testing.T) {
	want := []string{"结局分类", "次数预算", "无进展检测", "组合结论"}
	got := map[string]int{}
	for _, row := range mutationMatrix {
		if row.criterion == "" || row.mutant == "" || row.pin == nil {
			t.Fatalf("矩阵行不完整（判据/变异/用例三缺一）：%+v", row)
		}
		got[row.mechanism]++
	}
	for _, m := range want {
		if got[m] == 0 {
			t.Fatalf("机制分组 %q 在矩阵里一行都没有（新增机制却忘了钉反向验证 ⇒ 当天即红）", m)
		}
	}
}
