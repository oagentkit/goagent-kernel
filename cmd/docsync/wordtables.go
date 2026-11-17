// wordtables.go —— README 职责列里手抄的判定词表 ↔ 代码导出常量（issue #384）。
//
// README 包表的「职责」列是散文，docsync 此前明确不比对内容（只判非空）。
// 但其中有三张**判定词表**被逐字手抄：
//
//   - trajectory 行的工具调用配对状态（代码侧 Pairing* 常量）；
//   - watchdog 行的缺口 reason（代码侧工具级 Reason* 常量）；
//   - finalize 行的四分路由终态（代码侧 contract.Status* 常量）。
//
// 这三张表在代码侧都有导出常量（真相源），README 是第二份表达，却没有
// 任何机器判据——新增/改名一个取值，README 可以一直停在旧清单。
//
// # 判据
//
// 每个包声明：常量来源（目录 + 前缀/排除前缀/显式名单）+ README 行的抽取
// 正则。词表取值**从代码 AST 派生**，门禁里不抄任何字面量；否则门禁自己
// 也会漂。双向判：
//
//   - 漏列：词表的每个非空取值必须出现在抽取区域；
//   - 多列：抽取区域里的同族词必须都在词表内。
//
// 抽取区域由正则钉住（「配对状态（…）」「缺口按 … 告警」「四分路由（…）」），
// 不扫整段职责列——散文里的其它英文词不是词表成员。

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// readmeWordTable 一条「README 职责列 ↔ 代码词表」声明。
type readmeWordTable struct {
	// pkg README 包表行对应的包名。
	pkg string
	// sourceDir 导出常量所在目录（相对仓库根）。finalize 的终态词表定义在
	// contract，不在 finalize 自己包里。
	sourceDir string
	// constPrefix 采集的常量名前缀（与 constNames 二选一）。
	constPrefix string
	// excludePrefix 从 constPrefix 结果中排除的前缀（watchdog 的轮级
	// ReasonTurn* 不属于工具调用级缺口词表）。
	excludePrefix string
	// constNames 显式列出的常量名（按名取，取值仍从代码 AST 派生）。
	constNames []string
	// extract 从职责列抽取手抄词表区域的正则，必须含一个捕获组。
	extract *regexp.Regexp
}

// readmeWordTables 当前受机器校验的三张手抄词表。
//
// 新增一张时只加声明：取值自动从常量派生，README 漏列/多列会红。
var readmeWordTables = []readmeWordTable{
	{
		pkg:         "trajectory",
		sourceDir:   "trajectory",
		constPrefix: "Pairing",
		// 空值 PairingNone="" 不是取值，派生时自动跳过。
		extract: regexp.MustCompile(`配对状态\*\*（([^）]*)）`),
	},
	{
		pkg:           "watchdog",
		sourceDir:     "watchdog",
		constPrefix:   "Reason",
		excludePrefix: "ReasonTurn",
		extract:       regexp.MustCompile(`缺口按 ([^，]*) 告警`),
	},
	{
		pkg:       "finalize",
		sourceDir: "contract",
		constNames: []string{
			"StatusCreated", "StatusCancelled", "StatusTimedOut", "StatusFailed",
		},
		extract: regexp.MustCompile(`四分路由\*\*（([^）]*)）`),
	},
}

// wordTokenRe 从抽取区域里取「同族词」：小写开头的 snake_case 标识符。
// 中文散文与 Markdown 标记不参与比对。
var wordTokenRe = regexp.MustCompile(`[a-z][a-z0-9_]*`)

// checkWordTables 校验声明表里的每张词表与 README 职责列逐值一致。
//
// 包表里没有该包时跳过：那是 run() 的「仓里有该包、README 未收录」判据
// 负责的另一种漂移，不在本函数重复报。
func checkWordTables(rows []row) ([]string, error) {
	byPkg := make(map[string]row, len(rows))
	for _, r := range rows {
		byPkg[r.pkg] = r
	}

	var problems []string
	for _, wt := range readmeWordTables {
		// 包表里没有该包时跳过：合成仓/最小仓没有这些包是合法状态（与 USAGE
		// 的跳过口径同款）；本仓若漏收录该包，由 run() 的反向判据负责。
		// 先判存在性再派生，避免对不存在的目录报「无法派生」把别的判据淹没。
		r, ok := byPkg[wt.pkg]
		if !ok {
			continue
		}
		values, err := deriveWordTable(wt)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("%s: 代码里没有取到任何词表常量（%s，prefix=%q / names=%v）——"+
				"声明面与代码脱钩了", wt.pkg, wt.sourceDir, wt.constPrefix, wt.constNames)
		}
		m := wt.extract.FindStringSubmatch(r.desc)
		if m == nil {
			problems = append(problems, fmt.Sprintf(
				"%s: 职责列里找不到词表区域（正则 %q）——结构被改了，门禁的抽取点失效",
				wt.pkg, wt.extract.String()))
			continue
		}
		listed := wordTokenRe.FindAllString(m[1], -1)
		listedSet := make(map[string]bool, len(listed))
		for _, tok := range listed {
			listedSet[tok] = true
		}

		valueSet := make(map[string]bool, len(values))
		for _, v := range values {
			valueSet[v] = true
			if !listedSet[v] {
				problems = append(problems, fmt.Sprintf(
					"%s: README 职责列漏列词表值 %q（代码导出常量有，README 没有）", wt.pkg, v))
			}
		}
		// 多列：区域里出现、但代码词表没有的同族词。按首次出现序报，稳定。
		seenExtra := map[string]bool{}
		for _, tok := range listed {
			if valueSet[tok] || seenExtra[tok] {
				continue
			}
			seenExtra[tok] = true
			problems = append(problems, fmt.Sprintf(
				"%s: README 职责列多列了词表外的词 %q（代码词表没有）", wt.pkg, tok))
		}
	}
	return problems, nil
}

// deriveWordTable 从代码 AST 派生一张词表的全部非空取值（排序去重）。
//
// 取值只从常量声明的字符串字面量（或同块内已解析的常量引用）派生；
// 遇到匹配名却不是字符串字面量时**响亮失败**——派生不出来就没有结论，
// 静默跳过等于让这张表静默失守。
func deriveWordTable(wt readmeWordTable) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(wt.sourceDir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("%s: 列目录失败: %w", wt.pkg, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: %s 下没有 .go 文件，无法派生词表", wt.pkg, wt.sourceDir)
	}

	fset := token.NewFileSet()
	resolver := newConstResolver(".")
	parsed := make(map[string]string) // 常量名 → 值
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, file, nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("%s: 解析 %s 失败: %w", wt.pkg, file, perr)
		}
		imports := fileImports(f)
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if !wt.wants(name.Name) {
						continue
					}
					expr := constExpr(vs, i)
					if expr == nil {
						return nil, fmt.Errorf("%s: 常量 %s 没有可派生的字符串值", wt.pkg, name.Name)
					}
					val, ok := resolver.constString(expr, wt.sourceDir, imports, parsed)
					if !ok {
						return nil, fmt.Errorf("%s: 常量 %s 不是可派生的字符串值（字面量 / 同包常量 / 单源引用），无法从代码派生词表", wt.pkg, name.Name)
					}
					parsed[name.Name] = val
				}
			}
		}
	}

	for _, name := range wt.constNames {
		if _, ok := parsed[name]; !ok {
			return nil, fmt.Errorf("%s: 声明的常量 %s 在 %s 里不存在（改名/删除后忘了同步声明）",
				wt.pkg, name, wt.sourceDir)
		}
	}

	var out []string
	seen := map[string]bool{}
	for _, v := range parsed {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	slices.Sort(out)
	return out, nil
}

// wants 判断常量名是否属于本词表。
func (wt readmeWordTable) wants(name string) bool {
	if len(wt.constNames) > 0 {
		return slices.Contains(wt.constNames, name)
	}
	if wt.constPrefix != "" && !strings.HasPrefix(name, wt.constPrefix) {
		return false
	}
	if wt.excludePrefix != "" && strings.HasPrefix(name, wt.excludePrefix) {
		return false
	}
	return wt.constPrefix != ""
}

// constExpr 取 ValueSpec 第 i 个名字对应的值表达式。
func constExpr(vs *ast.ValueSpec, i int) ast.Expr {
	switch {
	case len(vs.Values) == len(vs.Names):
		return vs.Values[i]
	case len(vs.Values) == 1:
		return vs.Values[0]
	default:
		return nil
	}
}
