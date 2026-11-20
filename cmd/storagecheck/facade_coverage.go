// facade_coverage.go —— 门面「证据覆盖」（F4）：面访问器要么有能力落点，要么登记。
//
// 为什么单独成文件：F4 判的是**证据面**（能力证据 + known_unbacked_accessors 登记），
// 与「代码派生集」无关 ⇒ 它自成一块；放在同一文件里时，改登记过期口径会顺手改到
// 派生扫描，而结构阈值只剩几行余量 ⇒ 任何改动都要靠折行腾位置。
//
// ⚠ 本文件由 cmd/storagecheck/facade.go **原样搬移**而来：判定逻辑与报错文案逐字未变。
package main

import (
	"fmt"
	"slices"
	"strings"
)

// checkFacadeCoverage F4：每个面访问器都要有能力落点或登记。
//
// 登记面本身也要判：存储必须存在且有门面、访问器必须落在该门面的 accessors 列表里、
// 理由必须写、不得重复登记、不得**过期**（已被证据覆盖 ⇒ 当天删掉）。
func checkFacadeCoverage(sel *Selection, order []string, facades map[string]*facadeDecl) []string {
	var probs []string
	registered := map[string]map[string]bool{}
	seen := map[string]bool{}
	for _, r := range sel.KnownUnbackedAccessors {
		where := fmt.Sprintf("%s: known_unbacked_accessors %s/%s", selectionPath, r.Storage, r.Accessor)
		st, ok := findStorage(sel.Storages, r.Storage)
		if !ok {
			probs = append(probs, where+" 引用了未登记的存储")
			continue
		}
		if st.Facade == nil {
			probs = append(probs, where+" 的存储没有门面声明（登记必须挂在有门面的存储上）")
			continue
		}
		if strings.TrimSpace(r.Reason) == "" {
			probs = append(probs, where+" 缺 reason（登记必须写明事实理由）")
			continue
		}
		key := st.Facade.Package + "#" + st.Facade.Type
		fd, ok := facades[key]
		if !ok {
			continue // 门面声明本身非法，已由 collectFacades 报过
		}
		if !slices.Contains(fd.accessors, r.Accessor) {
			probs = append(probs, where+" 不在该门面的 accessors 列表里（登记项必须落在面访问器上）")
			continue
		}
		dupKey := r.Storage + "#" + r.Accessor
		if seen[dupKey] {
			probs = append(probs, where+" 重复登记")
			continue
		}
		seen[dupKey] = true
		if registered[key] == nil {
			registered[key] = map[string]bool{}
		}
		registered[key][r.Accessor] = true
	}
	for _, key := range order {
		fd := facades[key]
		covered := coveredAccessors(sel, fd)
		var missing []string
		for _, a := range fd.accessors {
			if covered[a] || registered[key][a] {
				continue
			}
			missing = append(missing, a)
		}
		if len(missing) > 0 {
			probs = append(probs, fmt.Sprintf("%s: 门面 %s 的面访问器 %s 既没有能力落点，也没有登记进 known_unbacked_accessors", selectionPath, key, strings.Join(missing, "、")))
		}
		for _, a := range fd.accessors { // 登记过期即红
			if registered[key][a] && covered[a] {
				probs = append(probs, fmt.Sprintf("%s: 门面 %s 的面访问器 %s 已登记为「无能力落点」，但它已被能力证据覆盖（登记过期，当天删掉）", selectionPath, key, a))
			}
		}
	}
	return probs
}

// coveredAccessors 门面里被**能力证据**引用到的面访问器（共用门面的证据合起来算）。
func coveredAccessors(sel *Selection, f *facadeDecl) map[string]bool {
	out := map[string]bool{}
	for _, id := range f.storages {
		st, ok := findStorage(sel.Storages, id)
		if !ok {
			continue
		}
		for _, c := range st.Capabilities {
			for _, ev := range c.Evidence {
				path, symbol, found := strings.Cut(ev.Ref, "#")
				// ref 的 path 是**文件**路径，门面声明的 package 是**包目录**
				// ⇒ 判「落点文件在这个门面包里」，不是判两者字面相等。
				if !found || !underRoot(f.pkg, path) {
					continue
				}
				for _, a := range f.accessors {
					if symbol == f.typ+"."+a {
						out[a] = true
					}
				}
			}
		}
	}
	return out
}
