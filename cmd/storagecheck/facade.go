// facade.go —— 门面「同款集合」的相等性机判（台账 §28 建议 4）。
//
// 背景：examples/pgstore/store.go 断言「两侧门面暴露**同款集合** ⇒ 对位成为**事实**」，
// 可那条断言此前**没有**机器判据 ⇒ 删掉一侧的面访问器、全仓门禁照绿。
//
// 五条判据：
//
//	F1：代码派生集（go/ast）== accessors ∪ other_methods，**双向**差集都红。
//	    ⚠ 派生集不完整（嵌入字段 / 类型不是 struct 字面量）⇒ 直接判红（fail-closed）。
//	F2：accessors 非空、accessors ∩ other_methods == ∅、两个列表内部不重复。
//	F3：**各门面的 accessors 集合必须相等**（差集非空即红，点名差哪几个）。
//	F4：每个 accessor 要么被能力证据引用、要么登记进 known_unbacked_accessors
//	    ⇒ 都没有即红；登记若其实已被证据覆盖 ⇒ 红（过期登记当天即删）。
//	F5：声明面里出现**方法级落点**却没写 facade 段 ⇒ 红（防真空绿）。
//
// 判定单元是**门面**而不是存储：两个存储可以共用同一扇门（PostgreSQL 与
// shared-authority-pg 共用 examples/pgstore）⇒ 证据与登记都按门面归集。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oekit/pkg/errors"
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
	note := fmt.Sprintf("说明 门面 %s（%s）：%d 个面访问器 + %d 个其它导出方法（代码派生集 %d 个）", f.key, strings.Join(f.storages, "、"), len(f.accessors), len(f.others), len(methods))
	return probs, []string{note}
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

// facadeMethods 派生「门面包内某类型上的全部导出方法」，并报出该类型**派生集不完整**的原因。
//
// 口径：指定**包目录**下（**只取生产代码**，跳过 _test.go）指定**类型**上的全部
// **导出方法**。用 go/ast 判（与 symbols.go 同一口径）：逐行正则判不出「方法挂在
// 哪个接收者上」，而门面要的恰恰是「这一扇门上有什么」。接收者写法（*T / T）
// 由 receiverName 归一。
//
// ⚠ **派生集不完整**有两种形态，都报出来判红（fail-closed）：① **嵌入字段**——嵌入
// 类型的方法会被**提升**到门面类型；② **类型不是 struct 字面量**——**解析不出 struct
// 字段** ⇒ 嵌入字段看不见。两种形态下门面的导出方法集都比「直接声明的」大 ⇒ 漏算。
func facadeMethods(root, pkg, typ string) (map[string]bool, []string, error) {
	dir := filepath.Join(root, pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, errors.Wrap(err, "读门面包失败", "package", pkg)
	}
	out := map[string]bool{}
	var incomplete []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, nil, errors.Wrap(err, "解析门面包失败", "file", name)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 || receiverName(d.Recv.List[0].Type) != typ || !ast.IsExported(d.Name.Name) {
					continue
				}
				out[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name.Name != typ {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						// 类型别名 / 定义类型 ⇒ 解析不出 struct 字段 ⇒ 嵌入字段看不见
						// ⇒ 派生集不完整 ⇒ 不拿它去比（fail-closed）。
						shape := "定义类型，底层类型取自 " + types.ExprString(ts.Type)
						if ts.Assign.IsValid() {
							shape = "类型别名，指向 " + types.ExprString(ts.Type)
						}
						incomplete = append(incomplete, "门面类型 "+typ+" 不是 struct 字面量（"+shape+"）⇒ 解析不出 struct 字段 ⇒ 请写成 struct 字面量（或扩展本判据以解析别名/派生类型的结构）")
						continue
					}
					for _, fld := range st.Fields.List {
						if len(fld.Names) == 0 { // 匿名 ⇒ 嵌入 ⇒ 其方法被提升
							incomplete = append(incomplete, "含嵌入字段（"+types.ExprString(fld.Type)+"）⇒ 其导出方法集含提升方法 ⇒ 请改为显式委托方法（或扩展本判据以纳入提升方法）")
						}
					}
				}
			}
		}
	}
	return out, incomplete, nil
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
// 为什么先归一：[A,A] 与 [A,B] 逐元素包含判断会返回「相等」⇒ 两份不一样的清单
// 被判成一样 ⇒ 归一（去重 + 排序）后再比才是集合语义。
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
