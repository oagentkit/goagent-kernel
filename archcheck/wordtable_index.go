// wordtable_index.go —— 词表门禁的全仓索引：常量取值、import 别名、字符串字面量。
//
// # 为什么索引单独成文件
//
// 这套判据要回答三个**跨包**问题：
//
//  1. 主人包里那张表的**键**到底对应哪些取值（键是常量名，取值在常量声明里）；
//  2. 常量声明可能是**跨包别名**（statemachine 的 StatusCreated =
//     contract.StatusCreated），取值解析必须能追过去；
//  3. 某个包里有没有**手抄**了这些取值（要扫全部字符串字面量）。
//
// 三件事共用一份索引：同一件事扫两遍就多一份会漂移的实现，
// 而两份实现漂移时的表现是「门禁时红时绿」——最贵的失败形态。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// constDef 一个常量声明的取值形态（四态互斥）。
type constDef struct {
	literal   string // 直接写下的字符串取值
	localRef  string // 同包别名：指向本包另一个常量
	aliasPkg  string // 跨包别名：import 别名
	aliasName string // 跨包别名：常量名
}

// stringLit 一处字符串字面量。
type stringLit struct {
	path  string
	line  int
	pos   token.Pos
	value string
	// constDef 标记该字面量出现在**常量声明**里——那是本包给这个取值
	// **命名的定义处**。裸字面量只允许出现在这里（命名可以，写字面量不行）。
	constDef bool
}

// wordRange 一段源码区间（主人表的**定义处**豁免用）。
type wordRange struct {
	file  string
	start token.Pos
	end   token.Pos
}

// contains 报告某处字面量是否落在该区间内。
//
// 必须同文件才比：token.Pos 是 FileSet 里的全局偏移，跨文件比较会拿
// 另一个文件的偏移来比，结果是「恰好落在区间内」的假命中。
func (r wordRange) contains(path string, pos token.Pos) bool {
	if r.file == "" || r.file != path {
		return false
	}
	return pos >= r.start && pos < r.end
}

// wordPkg 一个包的生产代码事实。
type wordPkg struct {
	rel        string
	dir        string
	consts     map[string]constDef
	imports    map[string]string // import 别名 → import 路径
	qualified  map[string]bool   // "别名.名字" 形态的引用
	literals   []stringLit       // 全部字符串字面量（排除结构体标签与 import 路径）
	ownConsts  map[string]string // 本包自有常量：取值 → 常量名
	maps       map[string]*ast.CompositeLit
	slices     map[string]*ast.CompositeLit // 切片字面量（分组词表的载体形状）
	varRanges  map[string]wordRange         // 变量字面量的源码区间（表的定义处）
	funcs      map[string]*ast.FuncDecl
	methods    map[string]*ast.FuncDecl // 方法：键是「类型名.方法名」
	funcRanges map[string]wordRange     // 函数体的源码区间
}

// wordRepo 全仓索引。
type wordRepo struct {
	modulePath string
	byRel      map[string]*wordPkg
	byPath     map[string]*wordPkg
}

// buildRepoIndex 扫全仓生产代码，建一份索引。
//
// 取文件走的是**既有那套** iterFiles（与 deps.go 的依赖方向门禁同款）：
// 它处理了隐藏目录、嵌套模块（另一棵模块树）与测试文件三件事，且遍历失败
// 会响亮报错，而不是给出一份「尽力而为」的清单——那份清单会让门禁在
// 读不到子树时照常报绿。
func buildRepoIndex(root string) (*wordRepo, error) {
	files, err := iterFiles(root, []string{"*.go"}, true)
	if err != nil {
		return nil, err
	}
	modPath, err := moduleImportPath(root)
	if err != nil {
		return nil, fmt.Errorf("词表门禁读取 module 路径失败: %w", err)
	}
	fset := token.NewFileSet()
	idx := &wordRepo{
		modulePath: modPath,
		byRel:      map[string]*wordPkg{},
		byPath:     map[string]*wordPkg{},
	}
	byDir := map[string][]string{}
	for _, f := range files {
		d := filepath.Dir(f)
		byDir[d] = append(byDir[d], f)
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		p := newWordPkg(rel, dir)
		for _, f := range byDir[dir] {
			if err := collectPkgFile(p, f, fset); err != nil {
				return nil, err
			}
		}
		idx.byRel[rel] = p
		if rel == "." {
			idx.byPath[modPath] = p
			continue
		}
		idx.byPath[modPath+"/"+rel] = p
	}
	return idx, nil
}

// newWordPkg 建一个空的包索引。
func newWordPkg(rel, dir string) *wordPkg {
	return &wordPkg{
		rel:        rel,
		dir:        dir,
		consts:     map[string]constDef{},
		imports:    map[string]string{},
		qualified:  map[string]bool{},
		ownConsts:  map[string]string{},
		maps:       map[string]*ast.CompositeLit{},
		slices:     map[string]*ast.CompositeLit{},
		varRanges:  map[string]wordRange{},
		funcs:      map[string]*ast.FuncDecl{},
		methods:    map[string]*ast.FuncDecl{},
		funcRanges: map[string]wordRange{},
	}
}

// collectPkgFile 解析一个文件，把它的事实并进包索引。
func collectPkgFile(p *wordPkg, path string, fset *token.FileSet) error {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("词表门禁解析 %s 失败: %w", path, err)
	}
	for _, imp := range file.Imports {
		pth, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		switch {
		case imp.Name != nil:
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				continue // 空白导入 / 点导入没法按名引用
			}
			p.imports[imp.Name.Name] = pth
		default:
			p.imports[importBase(pth)] = pth
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.CONST:
				collectConstDefs(d, p.consts, p.ownConsts)
			case token.VAR:
				collectVarMaps(d, p.maps, p.varRanges, path)
				collectVarSlices(d, p.slices, p.varRanges, path)
			}
		case *ast.FuncDecl:
			// 只收**包级**函数：枚举出口是包级导出符号，方法同名不算出口
			// （否则某个类型上恰好有个同名方法就会顶掉真正的出口）。
			if d.Recv == nil {
				p.funcs[d.Name.Name] = d
				p.funcRanges[d.Name.Name] = wordRange{file: path, start: d.Pos(), end: d.End()}
				continue
			}
			// 方法另收一份：判定谓词常是方法形态（StepStatus.Valid），
			// 键用「类型名.方法名」——与 policy 的 predicate 声明同形。
			if t := recvTypeName(d.Recv); t != "" {
				key := t + "." + d.Name.Name
				p.methods[key] = d
				p.funcRanges[key] = wordRange{file: path, start: d.Pos(), end: d.End()}
			}
		}
	}
	collectStringLiterals(fset, file, p)
	collectQualifiedRefs(file, p)
	return nil
}

// resolveFunc 在本包里解析一个**函数面**的名字：
// 包级函数（IsKnownStatus）或方法（StepStatus.Valid）。
func (p *wordPkg) resolveFunc(name string) *ast.FuncDecl {
	if fn, ok := p.funcs[name]; ok {
		return fn
	}
	if fn, ok := p.methods[name]; ok {
		return fn
	}
	return nil
}

// exportedFuncName 报告一个函数面名字的**末段**是否导出。
//
// 为什么看末段：方法形态（pkg.Type.Method）的可见性由方法名决定，
// 而类型名决定的是「能不能被引用」——policy 声明错类型名时，
// 「找不到」那条判据已经报红了，不必在这里重复报一次。
func exportedFuncName(name string) bool {
	base := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		base = name[i+1:]
	}
	return ast.IsExported(base)
}

// importBase 取 import 路径的最后一段（Go 的默认包名）。
func importBase(pth string) string {
	if i := strings.LastIndex(pth, "/"); i >= 0 {
		return pth[i+1:]
	}
	return pth
}

// resolveConst 解析一个常量的**字符串取值**，可跨包追别名。
//
// 为什么要跨包：词表主人的常量常常是别名（statemachine 的
// StatusCreated = contract.StatusCreated）。只在本包解析的话，
// statemachine 那两张表会派生不出任何取值——而「派生不出」若被当成
// 「没有手抄」，门禁就是**静默空转**：一条都判不了却照常报绿。
func (idx *wordRepo) resolveConst(rel, name string, depth int) (string, bool) {
	if depth > 8 {
		return "", false
	}
	p := idx.byRel[rel]
	if p == nil {
		return "", false
	}
	def, ok := p.consts[name]
	if !ok {
		return "", false
	}
	switch {
	case def.literal != "":
		return def.literal, true
	case def.localRef != "":
		return idx.resolveConst(rel, def.localRef, depth+1)
	case def.aliasPkg != "" && def.aliasName != "":
		pth, ok := p.imports[def.aliasPkg]
		if !ok {
			return "", false
		}
		tp := idx.byPath[pth]
		if tp == nil {
			return "", false
		}
		return idx.resolveConst(tp.rel, def.aliasName, depth+1)
	}
	return "", false
}

// referencesOwner 报告 p 是否引用了主人包 ownerRel 里名为 constName 的常量。
//
// 「引用」的形态是限定选择器（owner.Const）：它不是字面量，天然不构成
// 手抄——但它证明这个包**知道**主人那张表。
func (idx *wordRepo) referencesOwner(p *wordPkg, ownerRel, constName string) bool {
	if constName == "" {
		return false
	}
	pth := idx.pathOf(ownerRel)
	for alias, imported := range p.imports {
		if imported != pth {
			continue
		}
		if p.qualified[alias+"."+constName] {
			return true
		}
	}
	return false
}

// pathOf 返回一个包目录对应的 import 路径。
func (idx *wordRepo) pathOf(rel string) string {
	if rel == "." {
		return idx.modulePath
	}
	return idx.modulePath + "/" + rel
}

// packages 按目录名排序返回全仓包索引（输出顺序稳定 = 报告可复现）。
func (idx *wordRepo) packages() []*wordPkg {
	out := make([]*wordPkg, 0, len(idx.byRel))
	for _, p := range idx.byRel {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}
