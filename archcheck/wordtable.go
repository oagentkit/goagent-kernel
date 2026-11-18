// wordtable.go —— 判定词表登记门禁（word_tables 段的执行体，go/ast 判定）。
//
// # 这条门禁防的是什么
//
// 判定词表（会话状态、任务状态、环节类别……）是**决定结论**的取值集合：
// 宿主按一张表落库，内核按另一张表判状态，同一个状态在两处得到相反结论，
// 而没有任何机器能拦住。
//
// 此前内核侧已经补齐了「导出枚举出口」（issue #356：内核词表可导出），
// 但**消费侧**仍各写一份字面量：内核新增一个取值，消费方那份不会跟着变，
// 且没有任何判据会红。⇒ 这条门禁把「消费方必须**引用**而不是手抄」
// 变成机器判据。
//
// # 两条判据
//
//  1. harness-word-table-enum-missing —— 登记的词表必须有一个**导出枚举出口**，
//     且该出口必须**读同一张表**（不得另抄一份清单，否则它就是第二个真相源）。
//  2. harness-word-table-handcopy —— 别的包在生产代码里**手抄**了登记词表的取值。
//
// # 两类词表（kind）
//
// decision（缺省）：取值**决定结论**。消费方不得手抄。
// input：取值是**外部喂进来的词形**（归一词表）。消费方自由使用——
// 那是输入数据，不是判定依据；门禁只判「这张表有导出枚举出口，
// 且出口读的是同一张表」。
//
// 为什么必须分两类：把输入词表当判定词表登记会引出成片假阳
// （实测 protocol 的 43 个图表词形会命中 17 处，大半是 JSON 键、
// HTTP 响应体这类无关上下文）。而它们同样需要保护——
// 「这张归一表归 protocol 管、它的出口不许漂」正是要机判的。
//
// # 「手抄」的判据形态（为什么它可机判）
//
// 取值属于登记词表 ⇒ 出现它的那处代码必须是**有名字的**：
// 要么它在常量声明里（本包给这个取值起了名字），要么它是主人表自身的
// 定义处，要么它根本不是字面量而是 owner.Const 这样的引用。
// 其余 ⇒ 那段字面量没有名字，判定与取值各写一份，改一处漏一处。
//
// 为什么允许「本包自有常量」：同一个拼写在不同包里可以是不同概念
// （contract 的 "failed" 是会话状态，delegate 的 "failed" 是回执状态）。
// 要求它**命名**，而不是要求它**不许用**——否则合法用法也会被判红，
// 而判红到没人信它，它就等于没有。
//
// # 形状归 policy，执行体通用
//
// 哪些包有哪张表、表由哪个变量承载、枚举出口叫什么、这张表是判定词表
// 还是输入词表，全部由 policy 的 word_tables 段声明。本文件不出现任何
// 具体词表——换一个仓只需换 policy。
//
// # 边界（写明判不了什么）
//
//   - 只判**字面量**形态：常量、枚举、映射键都判不到（它们已经是名字了）；
//   - 判定面只含**生产代码**（测试文件不判）：测试夹具是临时的，
//     且把夹具纳入会逼出「只为跑用例而写的常量」噪声；
//   - 判据是**出现点级**的：同一包里既定义了常量、又写了同一取值的裸字面量，
//     后者判红——「命名过了却还写字面量」比「没命名」更该改。
//     但「这段字面量是否在**判定路径**上」需要 go/types 档的类型信息
//     （RunOptions.TypeAware），本轮做不到，故此处只判「有没有名字」；
//   - 词表取值集合由**代码派生**（表的键 → 常量声明 → 常量取值），
//     policy 里不写取值清单：那份清单自己就是第二份会漂移的真相。
package archcheck

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

// 词表门禁的两条规则 ID（内置规则，不在 policy 的 rules 段里）。
const (
	wordTableEnumRuleID     = "harness-word-table-enum-missing"
	wordTableHandCopyRuleID = "harness-word-table-handcopy"
	wordTableFamilyRuleID   = "harness-word-table-family-uncovered"
)

// wordTableFacts 一张登记词表派生出来的事实。
type wordTableFacts struct {
	pkg     string            // 主人包（相对仓库根）
	backing string            // 承载取值的那张表（包级变量名）
	enum    string            // 导出枚举出口（函数名）
	kind    string            // 词表类别（decision / input）
	values  map[string]string // 取值 → 主人侧的常量名（空 = 该取值没有对应常量）
	// keyNames 这张表的**键**（常量名形态）。族覆盖判据要按名比对
	// （表里的键必须属于族前缀），所以键名要单独留着。
	keyNames map[string]bool
	// family 族前缀（policy 的 family 字段）：主人包里同族常量的前缀。
	// 形态是**一个或多个**前缀——一族的常量不一定共享同一个前缀。
	family FamilyPrefixes
	// familyExclude 族内有意不进这张表的一段常量（按前缀豁免）。
	familyExclude []WordTableFamilyExclude
	// defRanges 这张表的**定义处**区间：主人包在这段区间里写下的字面量
	// 是「这张表的定义」，不是手抄。
	defRanges []wordRange
}

// wordTableViolations 判定词表登记面的两条判据。
//
// 返回 error 表示门禁**给不出结论**（源码解析失败）——调用方必须据此非零
// 退出，绝不能当成「无违反」继续。
func wordTableViolations(root string, pol *Policy) ([]Violation, error) {
	if pol.WordTables == nil || !pol.WordTables.on() {
		return nil, nil
	}
	seg := *pol.WordTables
	if len(seg.Tables) == 0 {
		return nil, nil
	}
	idx, err := buildRepoIndex(root)
	if err != nil {
		return nil, err
	}
	policyPath := PolicyPath(root, pol)
	var out []Violation
	facts := make([]wordTableFacts, 0, len(seg.Tables))
	for _, t := range seg.Tables {
		f, drift := deriveWordTable(idx, t)
		if drift != "" {
			// 登记与事实偏离 = 这条登记**静默失管**（表改名、出口读错表、
			// 包迁走）。一律报红：门禁的失败形态必须是变红，不能是变绿。
			out = append(out, Violation{
				Rule: wordTableEnumRuleID,
				Path: policyPath,
				Text: drift,
				Kind: "word_table_decl_drift",
			})
			continue
		}
		facts = append(facts, f)
	}
	rels := buildWordTableRelations(seg.Relations)
	out = append(out, wordTableHandCopies(root, idx, facts, seg, rels)...)
	// 族覆盖判据（反向派生）：族成员漏进表 ⇒ 它的消费侧从此没人管。
	out = append(out, wordTableFamilyViolations(idx, facts, seg, policyPath)...)
	// 同形撞车判据：两张判定词表同形却没有声明关系 ⇒ 没人说得清
	// 「这处字面量想说的是哪张表」，手抄指路只能瞎猜。
	out = append(out, wordTableCollisionViolations(facts, rels, policyPath)...)
	// 关系锚点判据：声明指向的具体常量还对不对（改名/被删/取值漂了/说谎了）。
	out = append(out, wordTableAnchorViolations(idx, facts, seg.Relations, policyPath)...)
	// 函数面判据：判定谓词与输入归一出口是否与代码事实一致
	// （登记了就得守住；input 类缺归一出口也红）。
	out = append(out, wordTableFuncFaceViolations(root, idx, pol, seg)...)
	return out, nil
}

// deriveWordTable 从主人包派生一张登记词表的事实。
//
// 返回的 drift 非空 = **登记与事实已偏离**：包改名、表改名、枚举出口读错了
// 表——每一种都会让这条登记静默失管，故一律报红而不是跳过。
func deriveWordTable(idx *wordRepo, t WordTable) (wordTableFacts, string) {
	f := wordTableFacts{
		pkg:           t.Pkg,
		backing:       t.Backing,
		enum:          t.Enum,
		kind:          t.wordTableKind(),
		values:        map[string]string{},
		keyNames:      map[string]bool{},
		family:        t.Family,
		familyExclude: t.FamilyExclude,
	}
	// kind 的值域必须**封闭**：写错的类别名若被当成「不是 decision」，
	// 这条登记就静默降级成「不判消费方」——门禁的失败形态必须是变红。
	//
	// 这条判据归**自校验**（声明面内部矛盾，恒开、提交前就拦）；
	// 执行体这边只负责「不把写错的类别当成合法类别继续派生」。
	if f.kind != wordTableKindDecision && f.kind != wordTableKindInput {
		return f, ""
	}
	owner := idx.byRel[t.Pkg]
	if owner == nil {
		return f, "word_tables 段登记的词表包 " + t.Pkg +
			" 不存在——包改名/迁走会让这条登记静默失管（登记与事实已偏离）"
	}
	fn := owner.funcs[t.Enum]
	if fn == nil {
		return f, "word_tables 段登记的词表 " + t.Pkg + " 没有导出枚举出口 " + t.Enum +
			"——「这张表有哪些值」只有一个对外答案，缺了它下游只能手抄"
	}
	if !funcReturnsSlice(fn) {
		return f, "枚举出口 " + t.Pkg + "." + t.Enum + " 的返回值不是切片：它给不出「这张表有哪些值」"
	}
	var backingLit *ast.CompositeLit
	if t.Backing != "" {
		// 表的载体形状有两种：扁平 map（一张表）与切片（**分组**表，
		// 一张表分成若干组）。两者都算「一张表」，形状不该决定判不判。
		backingLit = owner.maps[t.Backing]
		if backingLit == nil {
			backingLit = owner.slices[t.Backing]
		}
		if backingLit == nil {
			return f, "word_tables 段登记的 backing 变量 " + t.Pkg + "." + t.Backing +
				" 不存在——表改名会让这条登记静默失管（登记与事实已偏离）"
		}
		if !bodyReferences(fn, t.Backing) {
			return f, "枚举出口 " + t.Pkg + "." + t.Enum + " 没有读登记的那张表 " + t.Backing +
				"——它读的是另一份清单，等于第二个真相源"
		}
		f.defRanges = append(f.defRanges, owner.varRanges[t.Backing])
	}

	if t.Backing != "" {
		lit := backingLit
		if _, ok := lit.Type.(*ast.ArrayType); ok {
			// 切片载体有两种形状，**必须按元素形状分**：
			//
			//  - 元素是**常量名**（[]Action{ActionForceRelease, ...}）：
			//    这是一张**扁平表**——取值就是那些常量，可派生，与 map 形状
			//    （键→取值）等价，可以登记成判定词表；
			//  - 元素是**复合字面量**（[]struct{...}{{...}}）：这才是**分组表**
			//    （一张表分成若干组），取值在组内，顶层派生不出。
			//
			// 之前把两者混为一谈 ⇒ controlplane 的介入动作表（常量切片）
			// 被误判成「分组表」而登记不进来。形状不决定判不判，
			// 只决定**能不能派生取值**。
			if !sliceHoldsConstNames(lit) {
				if f.kind == wordTableKindDecision {
					return f, "word_tables 段登记的判定词表 " + t.Pkg + "." + t.Backing +
						" 是**分组形状**（切片元素不是常量名）：取值派生只认扁平表——" +
						"若这张表是输入归一词表，请写 kind: input"
				}
				// 分组形状的**输入归一词表**：词形在分组内部，顶层派生不出取值。
				// 不派生 ≠ 不判——上面已判「表在、出口在、出口读同一张表」。
				return f, ""
			}
			if drift := f.deriveConstNames(idx, owner.rel, lit.Elts,
				"word_tables 段登记的词表 "+t.Pkg+"."+t.Backing+" 里的常量 "); drift != "" {
				return f, drift
			}
		} else {
			for _, kv := range lit.Elts {
				kve, ok := kv.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				switch key := kve.Key.(type) {
				case *ast.BasicLit:
					if key.Kind != token.STRING {
						continue
					}
					v, err := strconv.Unquote(key.Value)
					if err != nil || v == "" {
						continue
					}
					f.values[v] = "" // 键是字面量 ⇒ 主人包里没有对应常量可引
				case *ast.Ident:
					v, ok := idx.resolveConst(owner.rel, key.Name, 0)
					if !ok || v == "" {
						return f, "word_tables 段登记的词表 " + t.Pkg + "." + t.Backing +
							" 的键 " + key.Name + " 解析不出字符串取值（常量不在本仓、或不是字符串常量）"
					}
					f.values[v] = key.Name
					f.keyNames[key.Name] = true
				}
			}
		}
	} else {
		lit := returnedSliceLit(fn)
		if lit == nil {
			return f, "枚举出口 " + t.Pkg + "." + t.Enum +
				" 没有返回一个切片字面量：取值来源无法判定"
		}
		f.defRanges = append(f.defRanges, wordRange{
			file:  owner.funcRanges[t.Enum].file,
			start: lit.Pos(),
			end:   lit.End(),
		})
		if drift := f.deriveConstNames(idx, owner.rel, lit.Elts,
			"枚举出口 "+t.Pkg+"."+t.Enum+" 里的常量 "); drift != "" {
			return f, drift
		}
	}
	if len(f.values) == 0 {
		return f, "word_tables 段登记的词表 " + t.Pkg + " 派生不出任何取值（登记与事实已偏离）"
	}
	// 注意：**输入归一词表也派生取值**（派生本身无害——它只是这张表的词形集合），
	// 只是消费方不被判（扫描面按 kind 跳过）。派生与判定分开，
	// 「这张表的词形有哪些」这件事就始终有答案，而登记漂移仍会被判红。
	return f, ""
}

// wordTableIgnored 报告包目录是否在豁免清单内（含子目录）。
func wordTableIgnored(dirs []string, rel string) bool {
	for _, d := range dirs {
		d = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(d)), "./")
		if d == "" {
			continue
		}
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// sliceHoldsConstNames 报告切片字面量的元素是否**全是常量名**。
//
// 为什么形状要分：切片承载的词表有两种形态——
//
//   - []Action{ActionForceRelease, ...}：元素是常量名 ⇒ **扁平表**，
//     取值就是那些常量，可派生（与 map 形状等价）；
//   - []struct{...}{{...}, {...}}：元素是复合字面量 ⇒ **分组表**，
//     一张表分成若干组，取值在组内，顶层派生不出。
//
// 两者混为一谈 ⇒ 常量切片被误判成分组表而登记不进来
// （实测：controlplane 的介入动作表就是这样被挡住的）。
func sliceHoldsConstNames(lit *ast.CompositeLit) bool {
	if len(lit.Elts) == 0 {
		return false
	}
	for _, el := range lit.Elts {
		if _, ok := el.(*ast.Ident); !ok {
			return false
		}
	}
	return true
}

// deriveConstNames 从一组常量名派生取值（扁平表的两种载体共用）。
//
// 解析不出取值时返回一句 drift 文案（不是 error）：调用方据此报红——
// 「派生不出」若被当成「没有手抄」，门禁就是**静默空转**。
func (f *wordTableFacts) deriveConstNames(idx *wordRepo, rel string, elems []ast.Expr, label string) string {
	for _, el := range elems {
		id, ok := el.(*ast.Ident)
		if !ok {
			continue
		}
		v, ok := idx.resolveConst(rel, id.Name, 0)
		if !ok || v == "" {
			return label + id.Name + " 解析不出字符串取值（常量不在本仓、或不是字符串常量）"
		}
		f.values[v] = id.Name
		f.keyNames[id.Name] = true
	}
	return ""
}

// funcReturnsSlice 判函数的返回值是不是切片。
func funcReturnsSlice(d *ast.FuncDecl) bool {
	if d.Type == nil || d.Type.Results == nil {
		return false
	}
	for _, res := range d.Type.Results.List {
		if _, ok := res.Type.(*ast.ArrayType); ok {
			return true
		}
	}
	return false
}

// bodyReferences 判函数体里有没有引用某个标识符。
func bodyReferences(d *ast.FuncDecl, name string) bool {
	if d.Body == nil {
		return false
	}
	found := false
	ast.Inspect(d.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// returnedSliceLit 取函数里 return 的切片字面量（没有则 nil）。
//
// 它是「枚举出口即取值来源」那种表的取值来源：出口直接列出了全部取值。
func returnedSliceLit(d *ast.FuncDecl) *ast.CompositeLit {
	if d.Body == nil {
		return nil
	}
	var out *ast.CompositeLit
	ast.Inspect(d.Body, func(n ast.Node) bool {
		if out != nil {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		cl, ok := ret.Results[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		if _, ok := cl.Type.(*ast.ArrayType); ok {
			out = cl
		}
		return true
	})
	return out
}
