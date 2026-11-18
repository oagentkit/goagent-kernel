// wordtable_index_collect.go —— 索引的采集器：从一个文件里抽出常量、变量与字面量。
//
// 为什么与索引主体分开成文件：索引主体是「事实的形状」（有哪些包、
// 怎么解析跨包别名），采集器是「怎么从源码取事实」。两者混在一个文件里时，
// 取事实的细节（哪些字面量算取值、哪处算命名处）会把形状盖住——而形状
// 恰恰是读这套门禁的人最需要先看懂的部分。
package archcheck

import (
	"go/ast"
	"go/token"
	"strconv"
)

// collectConstDefs 收一个 const 声明块里的常量。
//
// 顺带记下**取值 → 常量名**（ownConsts）：违规文案要能点名「本包该用哪个
// 常量」——只说「请命名」而不说「命名成什么」，读者还得自己去找。
func collectConstDefs(d *ast.GenDecl, out map[string]constDef, own map[string]string) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				break
			}
			def := constDefOf(vs.Values[i])
			out[name.Name] = def
			if def.literal != "" {
				if _, dup := own[def.literal]; !dup {
					own[def.literal] = name.Name
				}
			}
		}
	}
}

// constDefOf 判定一个常量声明的取值形态。
func constDefOf(expr ast.Expr) constDef {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if v, err := strconv.Unquote(e.Value); err == nil {
				return constDef{literal: v}
			}
		}
	case *ast.Ident:
		return constDef{localRef: e.Name}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return constDef{aliasPkg: id.Name, aliasName: e.Sel.Name}
		}
	}
	return constDef{}
}

// collectVarMaps 收 var 声明里的 map 字面量（键即词表取值的来源）。
//
// 顺带记下字面量的源码区间：主人表若用**字面量键**（protocol 的图表词表
// 就是这个形状），那些字面量是「这张表的定义」而不是手抄——豁免要精确到
// 这一处区间，豁免整个主人包则会把主人包里的其它字面量一并放过。
func collectVarMaps(d *ast.GenDecl, out map[string]*ast.CompositeLit, ranges map[string]wordRange, path string) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Values) == 0 {
			continue
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		if !ok {
			continue
		}
		if _, ok := lit.Type.(*ast.MapType); !ok {
			continue
		}
		for _, n := range vs.Names {
			out[n.Name] = lit
			ranges[n.Name] = wordRange{file: path, start: lit.Pos(), end: lit.End()}
		}
	}
}

// collectVarSlices 收 var 声明里的切片字面量（分组词表的载体形状：
// 一张表分成若干组，每组一张词表）。
func collectVarSlices(d *ast.GenDecl, out map[string]*ast.CompositeLit, ranges map[string]wordRange, path string) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Values) == 0 {
			continue
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		if !ok {
			continue
		}
		if _, ok := lit.Type.(*ast.ArrayType); !ok {
			continue
		}
		for _, n := range vs.Names {
			out[n.Name] = lit
			ranges[n.Name] = wordRange{file: path, start: lit.Pos(), end: lit.End()}
		}
	}
}

// collectStringLiterals 收文件里的字符串字面量，**排除**结构体标签与 import 路径。
//
// 排除这两者的理由：它们不是「取值」，是**键名**——yaml:"rule" 里的
// "rule" 与某个词表取值拼写相同，但它不是那个概念；把它算进手抄，
// 判据就成了拼写匹配器。
//
// 顺带标出每处字面量是不是**命名处**（出现在 const 声明里）：
// 包级 const 与函数内局部 const 都算——局部 const 同样是「给它起了个名字」，
// 把它判红会让「在函数里起个局部名」这种正常写法被拦。
func collectStringLiterals(fset *token.FileSet, file *ast.File, p *wordPkg) {
	excluded := map[token.Pos]bool{}
	constLits := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			if x.Tag != nil {
				excluded[x.Tag.Pos()] = true
			}
		case *ast.ImportSpec:
			if x.Path != nil {
				excluded[x.Path.Pos()] = true
			}
		case *ast.GenDecl:
			if x.Tok != token.CONST {
				return true
			}
			for _, spec := range x.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						constLits[lit.Pos()] = true
					}
				}
			}
		}
		return true
	})
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || excluded[lit.Pos()] {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		p.literals = append(p.literals, stringLit{
			path:     fset.Position(lit.Pos()).Filename,
			line:     fset.Position(lit.Pos()).Line,
			pos:      lit.Pos(),
			value:    v,
			constDef: constLits[lit.Pos()],
		})
		return true
	})
}

// collectQualifiedRefs 收 "别名.名字" 形态的引用（跨包常量引用的证据）。
func collectQualifiedRefs(file *ast.File, p *wordPkg) {
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			p.qualified[id.Name+"."+sel.Sel.Name] = true
		}
		return true
	})
}
