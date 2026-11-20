// writers.go —— 写者数的**交叉校验**（W1 存储侧 / W2 场景侧）。
//
// # 为什么需要「交叉」校验
//
// 声明面里有两张与写者有关的表：
//
//   - scenario_writers：场景形态 → 该形态的并发写者数；
//   - max_writers：存储 → 该存储能承受的并发写者数。
//
// 原先只判「场景写者数 > 存储上限」⇒ 这条判据**只由一张表撑着**：把存储
// 上限从 1 改成 2，边界就整个失效（评审实测：改弱任一张表 ⇒ 门禁 rc=0）。
// 两张表**互相冗余**却没人把它们钉在一起 ⇒ 各自单独改弱都能让一条注入变绿。
//
// ⇒ 两条表必须**互相钉住**：
//
//	W1（存储侧）：存储的写者上限是多写者档位（≥2 或 unlimited）
//	    ⟺ 它认领了至少一项多写者能力；
//	W2（场景侧）：形态写者数 ≥2 ⟺ 该形态下至少一个场景的 required 含多写者
//	    能力；形态写者数 ==1 ⇒ 该形态下**所有**场景的 required 都不含多写者
//	    能力。
//
// # 「多写者能力」从矩阵派生，不在代码里写死
//
// 多写者集合 = 矩阵里标了 requires_multi_writer: true 的能力 ⇒ 代码里**没有**
// 任何能力 id ⇒ 矩阵改了多写者清单，门禁跟着改（不会「代码里的清单过期」）。
//
// # 本文件另外判两条（与 W1/W2 同源，口径一致）
//
//   - W3（陈旧形态键）：scenario_writers 里没有任何场景引用的形态键 ⇒ 红
//     （与「陈旧存储当天即红」同款：没人引用的声明躺在那里 = 有人以为它还有效）。
//     两个方向合起来才是「两张表互洽」：矩阵里的形态必须登记写者数，登记的
//     形态必须被矩阵引用。
//   - 写者上限非法**按存储报一次**（同一存储被 N 个场景引用时，同一条错误会
//     按场景报 N 遍 ⇒ 报告里全是同文噪声，压过信号）。
package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// checkMultiWriterAnnotations 矩阵里每条能力都必须**显式**标注 requires_multi_writer。
//
// 缺省即红：漏标注 ⇒ 该能力既不在多写者集合、也不在单写者集合里 ⇒ W1/W2
// 对它**无从下手**（静默失效）⇒ 那正是最贵的形态（「从没触发过」等于没有）。
func checkMultiWriterAnnotations(m *Matrix) []string {
	var probs []string
	for _, c := range m.Capabilities {
		if c.RequiresMultiWriter == nil {
			probs = append(probs, fmt.Sprintf("%s: 能力 %q 没有标注 requires_multi_writer（缺省即红：漏登记 = 判据失效）",
				matrixPath, c.ID))
		}
	}
	return probs
}

// multiWriterCapabilities 派生多写者能力集合（从矩阵派生）。
//
// 返回集合而不是切片：判定只需要「是不是多写者能力」。
func multiWriterCapabilities(m *Matrix) map[string]bool {
	out := map[string]bool{}
	for _, c := range m.Capabilities {
		if c.RequiresMultiWriter != nil && *c.RequiresMultiWriter {
			out[c.ID] = true
		}
	}
	return out
}

// checkStorageWriterTier 写者数交叉校验的**存储侧**（W1）。
//
// 存储的写者上限与它认领的能力必须自洽：多写者档位 ⟺ 至少认领一项多写者能力。
//
// 两个方向都要判：只判「多写者档位却没认领多写者能力」会漏掉反方向
// （单写者档位却认领多写者能力 = 声明面在吹牛）。
func checkStorageWriterTier(sel *Selection, multi map[string]bool) []string {
	var probs []string
	for _, st := range sel.Storages {
		limit, unbounded, err := parseMaxWriters(st.MaxWriters)
		if err != nil {
			continue // 上限本身非法，已由 checkWriters 报过（不重复报）
		}
		var claimed []string
		for _, c := range st.Capabilities {
			if multi[c.ID] {
				claimed = append(claimed, c.ID)
			}
		}
		multiTier := unbounded || limit >= 2
		switch {
		case multiTier && len(claimed) == 0:
			probs = append(probs, fmt.Sprintf(
				"%s: 存储 %q 的写者上限是多写者档位（%s），但它没有认领任何多写者能力",
				selectionPath, st.ID, describeMaxWriters(limit, unbounded)))
		case !multiTier && len(claimed) > 0:
			probs = append(probs, fmt.Sprintf(
				"%s: 存储 %q 只支持 %d 个并发写者，却认领了多写者能力 %s",
				selectionPath, st.ID, limit, strings.Join(claimed, "、")))
		}
	}
	return probs
}

// checkScenarioWriterFormsReferenced scenario_writers 里**没有任何场景引用**的
// 形态键 ⇒ 红（陈旧声明当天即红）。
//
// 为什么与「陈旧存储当天即红」同款：形态已从矩阵里删掉，写者数表却还留着
// 它 ⇒ 它躺在声明面里等人以为它还有效（下次有人「照着它加一个场景」就会
// 复活一个已经不存在的形态）。
//
// 两个方向合起来才是「两张表互洽」：矩阵里的每个形态都必须登记写者数
// （漏登记 ⇒ 硬边界无从下手，见 checkWriters），而登记的每个形态都必须被
// 矩阵引用（没人引用 ⇒ 陈旧声明）。只判一头 ⇒ 另一头可以静默腐烂。
//
// 报告顺序稳定：map 遍历顺序随机 ⇒ 先把陈旧键排序再逐条报（不然同一份声明面
// 每次报的行序都不同，diff 出来的噪声比信号多）。
func checkScenarioWriterFormsReferenced(m *Matrix, sel *Selection) []string {
	referenced := map[string]bool{}
	for _, sc := range m.Scenarios {
		referenced[sc.Worker] = true
	}
	var stale []string
	for form := range sel.ScenarioWriters {
		if !referenced[form] {
			stale = append(stale, form)
		}
	}
	sort.Strings(stale)
	var probs []string
	for _, form := range stale {
		probs = append(probs, fmt.Sprintf(
			"%s: 场景形态 %q 登记了写者数，但矩阵里没有任何场景引用它（陈旧声明当天即红）",
			selectionPath, form))
	}
	return probs
}

// checkScenarioWriterTier 写者数交叉校验的**场景侧**（W2）。
//
// 形态的并发写者数与该形态下场景**实际要求的能力**必须自洽。
//
// 报红时点名形态与具体场景：形态写者数 ≥2 却没有任何场景要求多写者能力 ⇒
// 说明那张表被改弱了（这时「多写者场景不得选单写者档位」已经没人撑）。
//
// 兼判 scenario_writers 表自身的陈旧键（W3，见 checkScenarioWriterFormsReferenced）：
// 场景侧判据读的就是这张表 ⇒ 表里没人引用的键由这里一并判红。
func checkScenarioWriterTier(m *Matrix, sel *Selection, multi map[string]bool) []string {
	var probs []string
	probs = append(probs, checkScenarioWriterFormsReferenced(m, sel)...)
	forms := map[string][]Scenario{}
	for _, sc := range m.Scenarios {
		forms[sc.Worker] = append(forms[sc.Worker], sc)
	}
	// 按矩阵里的场景出现顺序遍历形态（map 遍历随机 ⇒ 报告顺序会漂）。
	done := map[string]bool{}
	for _, sc := range m.Scenarios {
		if done[sc.Worker] {
			continue
		}
		done[sc.Worker] = true
		writers, ok := sel.ScenarioWriters[sc.Worker]
		if !ok {
			continue // 没登记写者数，已由 checkWriters 报过
		}
		if writers < 1 {
			probs = append(probs, fmt.Sprintf("%s: 场景形态 %q 的并发写者数写 %d（至少为 1）",
				selectionPath, sc.Worker, writers))
			continue
		}
		var withMulti []string
		for _, s := range forms[sc.Worker] {
			for _, req := range s.Required {
				if multi[req] {
					withMulti = append(withMulti, s.ID+"→"+req)
					break
				}
			}
		}
		switch {
		case writers >= 2 && len(withMulti) == 0:
			probs = append(probs, fmt.Sprintf(
				"%s: 场景形态 %q 登记了 %d 个并发写者，但它下面的场景都没有要求多写者能力（写者数表被改弱了）",
				selectionPath, sc.Worker, writers))
		case writers == 1 && len(withMulti) > 0:
			probs = append(probs, fmt.Sprintf(
				"%s: 场景形态 %q 只有 1 个并发写者，但它下面的场景要求了多写者能力 %s",
				selectionPath, sc.Worker, strings.Join(withMulti, "、")))
		}
	}
	return probs
}

// checkStorageMaxWriters 写者上限非法**按存储报一次**（去重）。
//
// 为什么：写者上限写在**存储**上，而场景循环是按场景走的 ⇒ 同一存储被 N 个
// 场景引用时，同一条错误会报 N 遍 ⇒ 报告里 N 条同文噪声压过信号（读者得
// 自己数一遍才知道那是同一件事）。报红时列出引用它的场景 ⇒ 一眼看出影响面。
//
// 判定面与旧实现**逐字一致**：只判「被至少一个**已登记写者数形态**的场景
// 引用」的存储（漏登记形态的场景由「没有登记写者数」那条判据管；存储不在
// 允许集内由判据 1 管）—— 本函数只改**报几次**，不改**判什么**。
//
// 报红按存储在声明面里的出现顺序（不按场景顺序）⇒ 同一份声明面的报告稳定。
func checkStorageMaxWriters(m *Matrix, sel *Selection) []string {
	// 引用该存储的全部场景（按矩阵里的出现顺序）。
	refs := map[string][]string{}
	// 是否被**已登记写者数形态**的场景引用（旧实现的触发条件，保持不变）。
	triggered := map[string]bool{}
	for _, sc := range m.Scenarios {
		if _, ok := findStorage(sel.Storages, sc.Storage); !ok {
			continue // 存储不在允许集内 ⇒ 判据 1 已报
		}
		refs[sc.Storage] = append(refs[sc.Storage], sc.ID)
		if _, ok := sel.ScenarioWriters[sc.Worker]; ok {
			triggered[sc.Storage] = true
		}
	}
	var probs []string
	for _, st := range sel.Storages {
		if !triggered[st.ID] {
			continue
		}
		if _, _, err := parseMaxWriters(st.MaxWriters); err != nil {
			probs = append(probs, fmt.Sprintf("%s: 存储 %q 的 %v（被场景 %s 引用）",
				selectionPath, st.ID, err, strings.Join(refs[st.ID], "、")))
		}
	}
	return probs
}

// describeMaxWriters 写者上限的原文（报告里要写清是哪个档位）。
func describeMaxWriters(limit int, unbounded bool) string {
	if unbounded {
		return writersUnlimited
	}
	return strconv.Itoa(limit)
}
