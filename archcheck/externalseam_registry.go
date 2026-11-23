// externalseam_registry.go —— 外部依赖清单的**生成**与**接缝符号核验**（issue #482）。
//
// # 为什么清单要能生成
//
// 清单是「盘点结果」，而盘点最怕的是**写完就烂**：代码里新增一处
// `time.Now()`，清单没跟上 ⇒ 「漏了谁」这件事又变成了「靠人记得」。
//
// ⇒ 清单由工具生成骨架（`make external-deps -update`），人只维护**判断**
// （role / seam / exempt / reason）：
//
//   - 已登记的条目：**判断字段原样保留**（刷新不覆盖人的判断）；
//   - 新出现的条目：role 留空 ⇒ **门禁判红**（未判定的外部触达不许进仓）；
//   - 消失的条目：删除（它已经不再问外部世界了；留着就是僵尸登记）。
//
// # 接缝符号为什么要核验
//
// 「这个函数有接缝」是一句**可被验证的话**才有价值。接缝字段指向包内的一个
// 符号（`Type.Field` 或顶层标识符），核验它存在 ⇒ 改名/搬走会当场变红，
// 而不是等到某天有人误以为接缝还在。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// extSeamExists 核验接缝符号在包里真实存在。
//
// 两种形态：
//   - `Type.Field`：包内声明了 `type Type struct{...}`，且它有 `Field` 这个字段；
//   - `Name`：包内有同名顶层声明（类型 / 变量 / 函数）。
//
// 返回 error 的文案直接进报告 ⇒ 要说清「缺的是什么」。
func extSeamExists(root, pkg, funcName, seam string) error {
	dir := filepath.Join(root, pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("包目录 %s 读不到: %w", pkg, err)
	}
	trimmed := strings.TrimSpace(seam)
	if strings.HasPrefix(trimmed, "param:") {
		return extSeamParamExists(root, pkg, funcName, strings.TrimPrefix(trimmed, "param:"))
	}
	parts := strings.SplitN(trimmed, ".", 2)
	fset := token.NewFileSet()
	declNames := map[string]bool{}
	structFields := map[string]map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		af, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if perr != nil {
			return fmt.Errorf("解析 %s 失败: %w", e.Name(), perr)
		}
		for _, dec := range af.Decls {
			switch d := dec.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						declNames[s.Name.Name] = true
						if st, ok := s.Type.(*ast.StructType); ok {
							fields := map[string]bool{}
							for _, f := range st.Fields.List {
								for _, n := range f.Names {
									fields[n.Name] = true
								}
							}
							structFields[s.Name.Name] = fields
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							declNames[n.Name] = true
						}
					}
				}
			case *ast.FuncDecl:
				declNames[d.Name.Name] = true
			}
		}
	}
	if len(parts) == 2 {
		typeName, field := parts[0], parts[1]
		fields, ok := structFields[typeName]
		if !ok {
			return fmt.Errorf("包里没有类型 %s（接缝字段 %q 挂在它上面）", typeName, seam)
		}
		if !fields[field] {
			return fmt.Errorf("类型 %s 没有字段 %s", typeName, field)
		}
		return nil
	}
	if !declNames[parts[0]] {
		return fmt.Errorf("包里没有名为 %s 的顶层声明", parts[0])
	}
	return nil
}

// extSeamParamExists 核验「形参接缝」：该函数确实有一个叫这个名字的形参。
//
// 形参是接缝的另一种形态（`func F(now func() time.Time)`）——它不落在包级
// 声明上，只能按**函数**核验。
func extSeamParamExists(root, pkg, funcName, param string) error {
	dir := filepath.Join(root, pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("包目录 %s 读不到: %w", pkg, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		af, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if perr != nil {
			return fmt.Errorf("解析 %s 失败: %w", e.Name(), perr)
		}
		for _, dec := range af.Decls {
			fd, ok := dec.(*ast.FuncDecl)
			if !ok || fd.Name.Name != funcName {
				continue
			}
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				name := extRecvType(fd.Recv.List[0].Type) + "." + fd.Name.Name
				if name != funcName && fd.Name.Name != funcName {
					continue
				}
			}
			if fd.Type.Params == nil {
				return fmt.Errorf("函数 %s 没有形参", funcName)
			}
			for _, p := range fd.Type.Params.List {
				for _, n := range p.Names {
					if n.Name == param {
						return nil
					}
				}
			}
			return fmt.Errorf("函数 %s 没有名为 %s 的形参", funcName, param)
		}
	}
	return fmt.Errorf("包里没有函数 %s", funcName)
}

// extUpdateRegistry 重新生成清单骨架（保留人的判断字段）。
//
// 流程：实测触达 → 与现有清单合并 → 写回。
// 合并规则（逐条写在这里，��为它是「刷新会不会覆盖人的判断」的唯一真相）：
//
//   - 实测里有的键：保留原条目的 role/seam/exempt/reason，deps 按实测刷新；
//   - 实测里没有的键：条目删除（它不再问外部世界）；
//   - 实测里新出现的键：新建条目，role 留空（⇒ 门禁判红，等人判定）。
//
// UpdateExternalDepsFromPolicy 按 policy 找到 external_seam 规则并刷新清单。
//
// 为什么走 policy 而不是写死路径：清单路径是**声明面**（宿主仓可以用自己的），
// 写死等于把宿主的事写进内核。
func UpdateExternalDepsFromPolicy(root string, pol *Policy) error {
	for _, r := range pol.Rules {
		if r.CheckKind == CheckKindExternalSeam {
			if err := UpdateExternalDeps(root, r); err != nil {
				return err
			}
			fmt.Printf("已刷新外部依赖清单 %s\n", r.Registry)
			return nil
		}
	}
	return fmt.Errorf("policy 里没有 check_kind %s 的规则：清单路径未知，刷新无从下手",
		CheckKindExternalSeam)
}

func UpdateExternalDeps(root string, r Rule) error {
	if strings.TrimSpace(r.Registry) == "" {
		return fmt.Errorf("规则 %s 没有 registry 字段：清单路径未知", r.ID)
	}
	touches, err := extTouches(root, r)
	if err != nil {
		return err
	}
	path := filepath.Join(root, r.Registry)
	old := map[extKey]ExternalDep{}
	if data, rerr := os.ReadFile(path); rerr == nil && len(strings.TrimSpace(string(data))) > 0 {
		var reg ExternalDepRegistry
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if derr := dec.Decode(&reg); derr != nil {
			return fmt.Errorf("解析现有清单失败（先修它再刷新）: %w", derr)
		}
		for _, d := range reg.Deps {
			old[extKey{d.Package, d.Function}] = d
		}
	}
	actual := map[extKey]map[string]bool{}
	var order []extKey
	for _, t := range touches {
		k := extKey{t.pkg, t.fn}
		if _, ok := actual[k]; !ok {
			actual[k] = map[string]bool{}
			order = append(order, k)
		}
		actual[k][t.class] = true
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].pkg != order[j].pkg {
			return order[i].pkg < order[j].pkg
		}
		return order[i].fn < order[j].fn
	})
	out := ExternalDepRegistry{Deps: []ExternalDep{}}
	for _, k := range order {
		d := old[k]
		d.Package = k.pkg
		d.Function = k.fn
		d.Deps = extClassList(actual[k])
		if d.Role == "" && len(d.Deps) > 0 {
			// 新条目：role 留空 ⇒ 门禁判红（未判定不许进仓）。
			d.Role = ""
		}
		out.Deps = append(out.Deps, d)
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	header := strings.Join([]string{
		"# 外部依赖清单（issue #482）——由 make external-deps -update 生成骨架。",
		"#",
		"# 人只维护**判断**字段：role（tool/stamp/decide）、seam（接缝符号）、",
		"# exempt（显式豁免理由）、reason（判定理由）。刷新**不会**覆盖这些字段。",
		"# 新出现的条目 role 留空 ⇒ 门禁判红，等人判定。",
		"#",
		"# 键 = 目录 + 函数名。deps 是实测值（刷新时按代码现状覆盖）。",
		"",
	}, "\n")
	return os.WriteFile(path, append([]byte(header), data...), 0o600)
}
