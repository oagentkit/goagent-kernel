// facade.go —— 门面「同款集合」的相等性机判（台账 §28 建议 4）。
//
// 背景：examples/pgstore/store.go 断言「两侧门面暴露**同款集合** ⇒ 对位成为**事实**」，可那条断言此前**没有**机器判据 ⇒ 删掉一侧的面访问器、全仓门禁照绿。
//
// 五条判据：
//
//	F1：代码派生集（go/ast）== accessors ∪ other_methods，**双向**差集都红。
//	    ⚠ 派生集不完整（类型声明不存在 / 嵌入字段 / 类型不是 struct 字面量）⇒ 直接判红（fail-closed）：这些形态下门面的导出方法集都比「直接声明的」大 ⇒ 漏算。
//	F2：accessors 非空、accessors ∩ other_methods == ∅、两个列表内部不重复。
//	F3：**各门面的 accessors 集合必须相等**（差集非空即红，点名差哪几个）。
//	F4：每个 accessor 要么被能力证据引用、要么登记进 known_unbacked_accessors
//	    ⇒ 都没有即红；登记若其实已被证据覆盖 ⇒ 红（过期登记当天即删）。
//	F5：声明面里出现**方法级落点**却没写 facade 段 ⇒ 红（防真空绿）。
//
// 判定单元是**门面**而不是存储：两个存储可以共用同一扇门（PostgreSQL 与 shared-authority-pg 共用 examples/pgstore）⇒ 证据与登记都按门面归集。
//
// 本文件只装**门面对位与派生集比较**（F1 / F2 / F3 / F5）与共享类型/助手：
// 派生集扫描（go/ast）在 facade_derive.go，证据覆盖（F4）在 facade_coverage.go。
// 拆分只为把「结构阈值余量」从 2 行拉回两位数，**判定语义与报错文案逐字未变**。
package main

import (
	"fmt"
	"slices"
	"strings"
)

// facadeDecl 一个门面的声明（若干存储可能共用同一份）。
type facadeDecl struct {
	key       string // "包目录#类型名"
	pkg       string
	typ       string
	accessors []string
	others    []string
	storages  []string // 声明了这个门面的存储 id（按出现顺序）
}

// checkFacades 门面侧的判据（F1–F5）。返回 (违反, 说明)；说明里带每个门面的**形状**。
func checkFacades(root string, sel *Selection) ([]string, []string) {
	var probs, notes []string
	facades, order := collectFacades(sel, &probs)
	for _, key := range order {
		probs = append(probs, checkFacadeLists(facades[key])...) // F2
		p, n := checkFacadeAgainstCode(root, facades[key])       // F1
		probs = append(probs, p...)
		notes = append(notes, n...)
	}
	probs = append(probs, checkFacadeParity(order, facades)...)        // F3
	probs = append(probs, checkFacadeCoverage(sel, order, facades)...) // F4
	probs = append(probs, checkFacadeDeclared(sel)...)                 // F5
	notes = append(notes, fmt.Sprintf("说明 门面判定面：%d 扇门面（%s）", len(order), joinOrNone(order)))
	return probs, notes
}

// collectFacades 归集声明面里的门面。共用同一门面的多个存储 ⇒ 合并成一份，
// 但两份声明必须一致（同一扇门写两套清单 = 声明面自相矛盾 ⇒ 红）。
func collectFacades(sel *Selection, probs *[]string) (map[string]*facadeDecl, []string) {
	facades := map[string]*facadeDecl{}
	var order []string
	for _, st := range sel.Storages {
		f := st.Facade
		if f == nil {
			continue
		}
		where := fmt.Sprintf("%s: 存储 %s 的门面声明", selectionPath, st.ID)
		if strings.TrimSpace(f.Package) == "" {
			*probs = append(*probs, where+" 缺 package（门面包目录，相对仓库根）")
			continue
		}
		if strings.TrimSpace(f.Type) == "" {
			*probs = append(*probs, where+" 缺 type（门面类型名）")
			continue
		}
		key := f.Package + "#" + f.Type
		fd, ok := facades[key]
		if !ok {
			facades[key] = &facadeDecl{key: key, pkg: f.Package, typ: f.Type,
				accessors: append([]string{}, f.Accessors...), others: append([]string{}, f.OtherMethods...),
				storages: []string{st.ID}}
			order = append(order, key)
			continue
		}
		if !sameStringSet(fd.accessors, f.Accessors) || !sameStringSet(fd.others, f.OtherMethods) {
			*probs = append(*probs, fmt.Sprintf("%s: 存储 %s 与存储 %s 共用门面 %s，但两份声明的面访问器/其它方法不一致（同一扇门只能有一份声明）", selectionPath, st.ID, strings.Join(fd.storages, "、"), key))
		}
		fd.storages = append(fd.storages, st.ID)
	}
	return facades, order
}

// checkFacadeLists F2：列表自身的形状。
func checkFacadeLists(f *facadeDecl) []string {
	var probs []string
	where := fmt.Sprintf("%s: 门面 %s", selectionPath, f.key)
	if len(f.accessors) == 0 {
		probs = append(probs, where+" 的 accessors 为空（门面至少要暴露一个面访问器）")
	}
	probs = append(probs, dupViolations(where+" 的 accessors", f.accessors)...)
	probs = append(probs, dupViolations(where+" 的 other_methods", f.others)...)
	for _, a := range f.accessors {
		if slices.Contains(f.others, a) {
			probs = append(probs, fmt.Sprintf("%s: %q 同时出现在 accessors 与 other_methods（面访问器与其余导出方法必须分开登记）", where, a))
		}
	}
	return probs
}

// checkFacadeAgainstCode F1：声明面与代码派生集**双向**比对。
func checkFacadeAgainstCode(root string, f *facadeDecl) ([]string, []string) {
	where := fmt.Sprintf("%s: 门面 %s", selectionPath, f.key)
	methods, incomplete, err := facadeMethods(root, f.pkg, f.typ)
	if err != nil {
		return []string{where + " 派生失败：" + err.Error()}, nil
	}
	// fail-closed：派生集不完整 ⇒ 不拿它判「逐字相符」，直接判红。
	if len(incomplete) > 0 {
		return []string{fmt.Sprintf("%s: 存储 %s 的门面类型 %s 派生集不完整（%s）⇒ 判红", where, strings.Join(f.storages, "、"), f.typ, strings.Join(incomplete, "；"))},
			[]string{fmt.Sprintf("说明 门面 %s（%s）：派生集不完整（%s）⇒ 已判红", f.key, strings.Join(f.storages, "、"), strings.Join(incomplete, "；"))}
	}
	declared := map[string]bool{}
	for _, a := range slices.Concat(f.accessors, f.others) {
		declared[a] = true
	}
	var extra, missing []string
	for m := range methods {
		if !declared[m] {
			extra = append(extra, m)
		}
	}
	for d := range declared {
		if !methods[d] {
			missing = append(missing, d)
		}
	}
	slices.Sort(extra)
	slices.Sort(missing)
	var probs []string
	if len(extra) > 0 {
		probs = append(probs, fmt.Sprintf("%s: 代码里有未登记的导出方法 %s（门面加了方法却没人登记 ⇒ 声明面已漂移）", where, strings.Join(extra, "、")))
	}
	if len(missing) > 0 {
		probs = append(probs, fmt.Sprintf("%s: 登记了 %s，但代码里没有这个导出方法（登记与代码不符）", where, strings.Join(missing, "、")))
	}
	return probs, []string{fmt.Sprintf("说明 门面 %s（%s）：%d 个面访问器 + %d 个其它导出方法（代码派生集 %d 个）", f.key, strings.Join(f.storages, "、"), len(f.accessors), len(f.others), len(methods))}
}

// checkFacadeParity F3：各门面的面访问器集合必须相等。
func checkFacadeParity(order []string, facades map[string]*facadeDecl) []string {
	var probs []string
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			a, b := facades[order[i]], facades[order[j]]
			onlyA := missingFrom(a.accessors, b.accessors)
			onlyB := missingFrom(b.accessors, a.accessors)
			if len(onlyA) == 0 && len(onlyB) == 0 {
				continue
			}
			probs = append(probs, fmt.Sprintf("%s: 门面 %s 与 %s 的面访问器集合不相等（%s 独有 %s；%s 独有 %s）——两侧必须同款", selectionPath, a.key, b.key, a.key, joinOrNone(onlyA), b.key, joinOrNone(onlyB)))
		}
	}
	return probs
}

// checkFacadeDeclared F5：有方法级落点的存储必须登记 facade 段。
//
// 为什么需要这条：F1–F4 都以「门面声明」为判定面 ⇒ 整段 facade: 被删掉时那些判据
// 无从下手（真空绿）。⇒ 用**派生面**兜住：能力证据里出现了该存储**自己代码根下**
// 的方法级落点（ref 的符号名带一个点 ⇒ 它是「类型.方法」形态）⇒ 该存储确实有门面。
func checkFacadeDeclared(sel *Selection) []string {
	var probs []string
	for _, st := range sel.Storages {
		if st.Facade != nil {
			continue
		}
		for _, c := range st.Capabilities {
			for _, ev := range c.Evidence {
				path, symbol, found := strings.Cut(ev.Ref, "#")
				if !found || !strings.Contains(symbol, ".") || !underRoot(st.CodeRoot, path) {
					continue
				}
				probs = append(probs, fmt.Sprintf("%s: 存储 %s 的能力 %q 的落点 %s 是**方法级**（门面访问器）⇒ 该存储必须登记 facade 段（否则「两侧同款」无人判定：真空绿）", selectionPath, st.ID, c.ID, ev.Ref))
			}
		}
	}
	return probs
}

// dupViolations 列表里的重复项。
func dupViolations(where string, list []string) []string {
	var probs []string
	seen := map[string]bool{}
	for _, v := range list {
		if seen[v] {
			probs = append(probs, fmt.Sprintf("%s 里有重复项 %q", where, v))
			continue
		}
		seen[v] = true
	}
	return probs
}

// missingFrom a 里没有出现在 b 里的那些元素（保序）。
func missingFrom(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

// sameStringSet 两个列表按**集合**比较是否相等（忽略顺序；重复项由 F2 单独判）。
//
// 为什么先归一：[A,A] 与 [A,B] 逐元素包含判断会返回「相等」⇒ 两份不一样的清单被判成一样 ⇒ 归一（去重 + 排序）后再比才是集合语义。
func sameStringSet(a, b []string) bool {
	return slices.Equal(normSet(a), normSet(b))
}

// normSet 归一：去重 + 排序（集合语义的可比形态）。
func normSet(list []string) []string {
	out := append([]string{}, list...)
	slices.Sort(out)
	return slices.Compact(out)
}

// joinOrNone 把一组名字连起来（空 ⇒ 「无」）。
func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "无"
	}
	return strings.Join(list, "、")
}
