// capabilities.go —— 存储选型判据的**能力侧**与**口径侧**判据（issue #424）。
//
// 这一侧判三件事：
//
//   - 判据 3：「某存储提供某能力」的断言必须能在代码里找到落点；
//   - 派生纪律：source 标注必须与落点位置对得上（存储自证型 / 内核交付型）；
//   - 口径：选型口径必须**看得见**（打出来）且必须含关键文本。
//
// 为什么口径也要机器判：口径写成散文 ⇒ 没人验证 ⇒ 它就只是一段装饰。
// 只判「非空」也不够——把口径删到只剩一句空话照样绿 ⇒ 关键文本逐条列在
// 声明面里（数据），由门禁逐条核对。
package main

import (
	"fmt"
	"strings"
)

// checkPolicy 核对选型口径：口径必须进输出，且必须含关键文本。
//
// 返回 (违反, 说明)。说明里带**口径正文**——派单要的「在 cmd/storagecheck 的
// notes/输出里让人看得见」那半边，就落在这里。
func checkPolicy(sel *Selection) ([]string, []string) {
	var probs []string
	var notes []string
	if strings.TrimSpace(sel.Policy.Notes) == "" {
		probs = append(probs, selectionPath+": policy.notes 为空（选型口径必须写下来：判据只管得了对应关系）")
		return probs, notes
	}
	notes = append(notes, "口径\n"+strings.TrimRight(sel.Policy.Notes, "\n"))
	if len(sel.Policy.RequiredTexts) == 0 {
		probs = append(probs, selectionPath+": policy.required_texts 为空（口径要点必须逐条列出来：判据不许靠人记）")
		return probs, notes
	}
	seen := map[string]bool{}
	for _, rt := range sel.Policy.RequiredTexts {
		where := fmt.Sprintf("%s: policy.required_texts %q", selectionPath, rt.ID)
		if rt.ID == "" {
			probs = append(probs, where+": id 为空")
			continue
		}
		if seen[rt.ID] {
			probs = append(probs, where+": id 重复")
			continue
		}
		seen[rt.ID] = true
		if len(rt.Keywords) == 0 {
			probs = append(probs, where+" 没有 keywords（要点必须指明口径里要含哪些文本）")
			continue
		}
		missing := 0
		for _, kw := range rt.Keywords {
			if !strings.Contains(sel.Policy.Notes, kw) {
				probs = append(probs, fmt.Sprintf("%s: policy.notes 里没有该要点要求的文本 %q", where, kw))
				missing++
			}
		}
		if missing == 0 {
			notes = append(notes, "口径要点 "+rt.ID)
		}
	}
	return probs, notes
}

// checkStorageCapabilities 核对一条存储的能力清单（判据 3 + 派生标注）。
//
// 返回 (违反, 该存储认领的能力 id)。
func checkStorageCapabilities(root string, st Storage, known map[string]bool) ([]string, []string) {
	var probs []string
	var ids []string
	for _, c := range st.Capabilities {
		where := fmt.Sprintf("%s: 存储 %s 的能力 %q", selectionPath, st.ID, c.ID)
		ids = append(ids, c.ID)
		if !known[c.ID] {
			probs = append(probs, where+" 不在部署矩阵的能力表里（能力词表不许在本文件里手写）")
		}
		if len(c.Evidence) == 0 {
			probs = append(probs, where+" 没有落点（派生不出：判据无从下手，不当「没有手抄」放过）")
			continue
		}
		switch c.Source {
		case sourceStorage, sourceKernel:
		case "":
			probs = append(probs, where+" 缺 source 标注（存储自证型 / 内核交付型必须显式标注）")
		default:
			probs = append(probs, fmt.Sprintf("%s 的 source 写 %q，只允许 %s / %s",
				where, c.Source, sourceStorage, sourceKernel))
		}
		for _, ev := range c.Evidence {
			ep := where + " 的落点 " + ev.Ref
			if ev.Kind != "contract" {
				probs = append(probs, ep+" 的证据类型只允许 contract")
				continue
			}
			ok, reason := resolveRef(root, ev.Ref)
			if !ok {
				probs = append(probs, ep+" "+reason)
				continue
			}
			path, _, _ := strings.Cut(ev.Ref, "#")
			if c.Source == sourceStorage && !underRoot(st.CodeRoot, path) {
				probs = append(probs, fmt.Sprintf("%s 标注为存储自证型，但落点 %s 不在该存储的代码根 %q 下",
					ep, path, st.CodeRoot))
			}
			if c.Source == sourceKernel && underRoot(st.CodeRoot, path) {
				probs = append(probs, fmt.Sprintf("%s 标注为内核交付型，但落点 %s 就在该存储的代码根 %q 下",
					ep, path, st.CodeRoot))
			}
		}
	}
	return probs, ids
}

// checkAsymmetries 核对已知不对称的登记（落点必须真实存在，且两侧互不相同）。
func checkAsymmetries(root string, sel *Selection) []string {
	var probs []string
	for _, a := range sel.Asymmetries {
		where := fmt.Sprintf("%s: 已知不对称 %q", selectionPath, a.ID)
		if a.Summary == "" {
			probs = append(probs, where+" 缺 summary（不对称必须有一句人能读的话）")
		}
		if len(a.Storages) < 2 {
			probs = append(probs, where+" 必须落在至少两个存储上（不对称是两侧之间的差异）")
		}
		seen := map[string]bool{}
		for _, id := range a.Storages {
			if seen[id] {
				probs = append(probs, fmt.Sprintf("%s 的存储清单里有重复项 %q（不对称是两侧之间的差异，不是自己与自己比）",
					where, id))
			}
			seen[id] = true
			if _, ok := findStorage(sel.Storages, id); !ok {
				probs = append(probs, fmt.Sprintf("%s 引用了未登记的存储 %q", where, id))
			}
		}
		if len(a.Evidence) == 0 {
			probs = append(probs, where+" 没有落点（派生不出）")
			continue
		}
		for _, ev := range a.Evidence {
			if ev.Kind != "contract" {
				probs = append(probs, where+" 的证据类型只允许 contract")
				continue
			}
			if ok, reason := resolveRef(root, ev.Ref); !ok {
				probs = append(probs, where+" 的落点 "+ev.Ref+" "+reason)
			}
		}
	}
	return probs
}

// underRoot 判 path 是否落在 root 之下（root 为空 ⇒ 谁都不在它下面）。
func underRoot(root, path string) bool {
	if root == "" {
		return false
	}
	return path == root || strings.HasPrefix(path, root+"/")
}
