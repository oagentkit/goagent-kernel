package roundguard_test

import "go/ast"

// ── 导出面计数口径（判据在 exportbudget_test.go，这里是它的执行面）──
//
// 口径（什么算、什么不算）写在门禁文件的头注里——那里是**规则的声明面**，
// 本文件是**规则怎么落地**。两者分开放的理由与本仓「声明面 / 执行面分离」同款：
// 规则改在声明面，执行面不动；反过来，执行面被放松时，门禁文件里的自检用例
// （TestCountingRules*）会当场变红。
//
// 为什么全走 go/ast 而不逐行看文本：import / 声明 / 字段 / 方法的书写形态远不止
// 一种，逐行正则必然漏，且漏得毫无声响（与本仓 archcheck 当年同一个病）。
//
// 包级模型（类型与方法声明的跨文件汇总）与 AST 辅助在 exportbudget_model_test.go。

// countExportedSymbols 数一个文件里的导出符号数（口径见门禁文件头注）。
func countExportedSymbols(f *ast.File, m *packageModel) int {
	n := 0
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				// 包级函数。
				if d.Name.IsExported() {
					n++
				}
				continue
			}
			// 导出方法：方法名导出 **且** 接收者类型名导出
			//（未导出类型上的方法宿主 import 不到——除非它被**提升**进某个导出
			// 类型，那一路在类型声明处按「提升方法」计，见 promotedMethods）。
			if d.Name.IsExported() && ast.IsExported(receiverTypeName(d.Recv)) {
				n++
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						n++
						n += m.countTypeMembers(s)
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() {
							n++
						}
					}
				}
			}
		}
	}
	return n
}

// countTypeMembers 数一个导出类型的**成员面**。
//
// 结构体：导出具名字段 + 导出嵌入字段 + 提升进来的导出方法。
// 接口：导出方法集 + 提升进来的导出方法。
func (m *packageModel) countTypeMembers(ts *ast.TypeSpec) int {
	switch t := ts.Type.(type) {
	case *ast.StructType:
		return m.countStructMembers(ts, t)
	case *ast.InterfaceType:
		return m.countInterfaceMembers(ts, t)
	}
	return 0
}

// countStructMembers 数导出结构体的成员面。
func (m *packageModel) countStructMembers(ts *ast.TypeSpec, st *ast.StructType) int {
	n := 0
	promoted := map[string]bool{}
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			// 嵌入字段。
			name := embeddedFieldName(field.Type)
			if ast.IsExported(name) {
				// 嵌入的**导出**类型：它的名字成为本包的导出名（+1）；它的导出方法
				// 已在**它自己的声明处**计过 ⇒ 不重复计（否则同一个方法被数两次）。
				n++
				continue
			}
			// 嵌入的**未导出**类型：宿主拿不到那个类型，但它的**导出方法被提升**
			// 进本结构体 ⇒ 宿主能调用 Outer.Promoted() ⇒ 属交付面。
			for meth := range m.promotedMethods(name, map[string]bool{}) {
				promoted[meth] = true
			}
			continue
		}
		for _, name := range field.Names {
			if name.IsExported() {
				n++
			}
		}
	}
	// 本类型自己声明的导出方法**遮蔽**提升上来的同名方法（Go 规则：浅层赢）
	// ⇒ 被遮蔽的那些不是新名字，不计。
	for meth := range promoted {
		if !m.methods[ts.Name.Name][meth] {
			n++
		}
	}
	return n
}

// countInterfaceMembers 数导出**接口的方法集**。
//
// 为什么接口方法必须计入：给导出接口加一个方法，宿主侧的实现方就被迫多实现
// 一个 ⇒ **破坏性变更**。只数「接口类型本身」的话，这类改动对计数完全不可见
// ⇒ 「精确计数」在一条增长路径上彻底失明。
func (m *packageModel) countInterfaceMembers(ts *ast.TypeSpec, it *ast.InterfaceType) int {
	n := 0
	promoted := map[string]bool{}
	for _, field := range it.Methods.List {
		if len(field.Names) == 0 {
			// 嵌入接口：其方法集被提升进本接口。
			name := embeddedFieldName(field.Type)
			if name != "" && !ast.IsExported(name) {
				// 本包内的**未导出**接口：它的导出方法被提升 ⇒ 属交付面。
				for meth := range m.promotedMethods(name, map[string]bool{}) {
					promoted[meth] = true
				}
			}
			// 导出的嵌入接口：其方法在**它自己的声明处**已计过（跨包嵌入的则不可
			// 从本包 AST 解析 ⇒ 不计，属口径的已知边界）。
			continue
		}
		for _, name := range field.Names {
			if name.IsExported() {
				n++
			}
		}
	}
	for meth := range promoted {
		if !m.methods[ts.Name.Name][meth] {
			n++
		}
	}
	return n
}

// promotedMethods 列出某个类型经嵌入链**提升**上来的导出方法名。
//
// 「提升」= Go 的方法提升：外层类型嵌入内层类型后，内层的导出方法成为外层的
// 方法。于是内层类型虽然未导出，宿主仍能通过外层（导出）类型调用它 ⇒ 属交付面。
//
// 遮蔽规则：浅层赢 ⇒ 合并深层结果时，已在浅层出现的名字不重复计。
// visited 防环（畸形文件里可能出现自嵌入链）。
func (m *packageModel) promotedMethods(typeName string, visited map[string]bool) map[string]bool {
	out := map[string]bool{}
	if typeName == "" || visited[typeName] {
		return out
	}
	visited[typeName] = true
	for name := range m.methods[typeName] {
		out[name] = true
	}
	ts, ok := m.types[typeName]
	if !ok {
		return out
	}
	for _, inner := range embeddedTypeNames(ts.Type) {
		if ast.IsExported(inner) {
			// 导出的嵌入类型：其方法已在它自己的声明处计过 ⇒ 不重复计。
			continue
		}
		for name := range m.promotedMethods(inner, visited) {
			if !out[name] {
				out[name] = true
			}
		}
	}
	return out
}

// embeddedTypeNames 列出一个类型（结构体或接口）里嵌入的类型名。
func embeddedTypeNames(expr ast.Expr) []string {
	var fields *ast.FieldList
	switch t := expr.(type) {
	case *ast.StructType:
		fields = t.Fields
	case *ast.InterfaceType:
		fields = t.Methods
	default:
		return nil
	}
	if fields == nil {
		return nil
	}
	var out []string
	for _, f := range fields.List {
		if len(f.Names) != 0 {
			continue // 具名字段不是嵌入
		}
		if name := embeddedFieldName(f.Type); name != "" {
			out = append(out, name)
		}
	}
	return out
}
