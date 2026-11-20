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
package main

import (
	"fmt"
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

// checkScenarioWriterTier 写者数交叉校验的**场景侧**（W2）。
//
// 形态的并发写者数与该形态下场景**实际要求的能力**必须自洽。
//
// 报红时点名形态与具体场景：形态写者数 ≥2 却没有任何场景要求多写者能力 ⇒
// 说明那张表被改弱了（这时「多写者场景不得选单写者档位」已经没人撑）。
func checkScenarioWriterTier(m *Matrix, sel *Selection, multi map[string]bool) []string {
	var probs []string
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

// describeMaxWriters 写者上限的原文（报告里要写清是哪个档位）。
func describeMaxWriters(limit int, unbounded bool) string {
	if unbounded {
		return writersUnlimited
	}
	return strconv.Itoa(limit)
}
