// scan.go —— 接缝枚举、调用点计数与约束成文判定（issue #400）。
package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// collect 扫描 root 并汇总接缝、调用点与约束成文情况。
func collect(root string) (Report, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Report{}, err
	}
	modPath, err := modulePath(abs)
	if err != nil {
		return Report{}, err
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: abs,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return Report{}, err
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		return Report{}, fmt.Errorf("类型检查失败：%d 个包有错误（见上）——扫描器拿不到结论时不得当作零接缝放行", n)
	}

	// 第一趟：枚举接缝声明（类型 + 文档）。
	infos := map[string]*Seam{}
	byType := map[*types.TypeName]*Seam{}
	for _, p := range pkgs {
		if p.Types == nil || p.Name == "main" {
			continue // 命令工具不是宿主接缝
		}
		docs := typeDocs(p)
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || !tn.Exported() || tn.IsAlias() {
				continue
			}
			if strings.HasSuffix(name, "Option") {
				continue // 配置函子（func(*config)）不是宿主接缝
			}
			kind := ""
			switch u := tn.Type().Underlying().(type) {
			case *types.Interface:
				if u.NumMethods() == 0 {
					continue // 空接口没有可成文的契约
				}
				kind = "interface"
			case *types.Signature:
				kind = "func"
			default:
				continue
			}
			id := shortPkgPath(p.PkgPath, modPath) + "." + name
			s := &Seam{ID: id, Kind: kind, Written: constraints(docs[tn])}
			infos[id] = s
			byType[tn] = s
		}
	}

	// 第二趟：类型感知地数调用点。
	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if selection, ok := p.TypesInfo.Selections[sel]; ok {
						recv := selection.Recv()
						if ptr, ok := recv.(*types.Pointer); ok {
							recv = ptr.Elem()
						}
						if named, ok := recv.(*types.Named); ok {
							if s, ok := byType[named.Obj()]; ok {
								s.Callsites++
								return true
							}
						}
					}
				}
				if tv, ok := p.TypesInfo.Types[call.Fun]; ok && tv.Type != nil {
					if named, ok := tv.Type.(*types.Named); ok {
						if s, ok := byType[named.Obj()]; ok {
							s.Callsites++
						}
					}
				}
				return true
			})
		}
	}

	report := Report{Schema: 1, Declarations: len(infos)}
	for _, s := range infos {
		report.Callsites += s.Callsites
		if len(s.Written) == len(constraintOrder) {
			report.FullyWritten++
		}
		report.Seams = append(report.Seams, *s)
	}
	sort.Slice(report.Seams, func(i, j int) bool { return report.Seams[i].ID < report.Seams[j].ID })
	return report, nil
}

// typeDocs 收集每个类型声明的文档文本（interface 连带方法注释）。
func typeDocs(p *packages.Package) map[*types.TypeName]string {
	out := map[*types.TypeName]string{}
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				tn, ok := p.TypesInfo.Defs[ts.Name].(*types.TypeName)
				if !ok {
					continue
				}
				var b strings.Builder
				doc := ts.Doc
				if doc == nil {
					doc = gd.Doc
				}
				if doc != nil {
					b.WriteString(doc.Text())
				}
				if it, ok := ts.Type.(*ast.InterfaceType); ok {
					for _, m := range it.Methods.List {
						if m.Doc != nil {
							b.WriteString(m.Doc.Text())
						}
						if m.Comment != nil {
							b.WriteString(m.Comment.Text())
						}
					}
				}
				out[tn] = b.String()
			}
		}
	}
	return out
}

// constraints 从文档文本里判定三条约束是否成文。
//
// 只认**显式**措辞（不得重入 / 不得阻塞 / panic 隔离及其同义写法）；「通知
// 不阻塞」「锁外调用」这类描述实现行为的句子不算成文——约束是给宿主实现的
// 要求，不是内核的自述。
func constraints(doc string) []string {
	var out []string
	if containsAny(doc, "不得重入", "不可重入", "禁止重入") {
		out = append(out, constraintReentry)
	}
	if containsAny(doc, "不得阻塞", "不可阻塞", "禁止阻塞") {
		out = append(out, constraintBlocking)
	}
	if containsAny(doc, "panic 隔离", "panic隔离", "panic 由内核隔离", "panic由内核隔离") {
		out = append(out, constraintPanic)
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
