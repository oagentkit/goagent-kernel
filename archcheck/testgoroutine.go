// testgoroutine.go —— 「用例内起的 goroutine 必须在用例返回前被等待」的判定体。
//
// # 这条规则判什么
//
// issue #428：测试用例里起的 goroutine 必须在用例返回前退出。否则它会在用例
// 交还资源（关存储、删临时目录）之后仍去动那些资源 ⇒ 典型症状是 SQLite(WAL)
// 在已删目录里重建 -wal/-shm ⇒ t.TempDir() 的 RemoveAll 报 directory not empty
// （门禁 cover-gate 段偶发红；单跑不复现 ⇒ 负载依赖型，不是回归）。
//
// 这条纪律在 v0.51.0 已由人工落到 9 处用例上，但**没有任何规则在查** ⇒ 同一个
// 形态可以随时再长出来。这条规则把那条纪律变成门禁。
//
// # 判定面（逐条口径）
//
//  1. 只扫**测试文件**：policy 的 only_test: true。生产代码的 goroutine 由
//     harness-no-bare-goroutine 管（裸 goroutine 的 panic 无法被外层 recover），
//     两条规则各管一面，不重叠。
//  2. 只判**测试函数**：名字以 Test / Benchmark / Fuzz 开头的包级函数。
//     TestMain 不在面内：它是整个测试二进制的入口，不是「一个用例」——它起的
//     goroutine 活到进程结束是设计，不是泄漏。
//  3. **触发**：测试函数体内有 go 语句（出现在函数体任意深度，含 t.Run 子用例
//     闭包内——子用例也是本进程里的 goroutine，同样不许越过用例边界）。
//  4. **豁免**：测试函数体内，或它调用的**包内函数**（同目录同包名，深度 ≤2）
//     里存在**等待点**。
//  5. **等待点**：接收表达式 <-ch，或调用 X.Wait()。
//
// # 为什么豁免必须 helper 感知（且要跟到深度 ≤2）
//
// 等它的收尾常常写在**助手函数**里（examples/sqliteadapter 的 stopTailer：
// cancel → <-done → 超时兜底）。调用点本身没有等待原语 ⇒ 只按测试函数体内的
// 等待原语判 ⇒ 把已经修好的用例判成违规（大量误报 ⇒ 门禁红到没人信它）。
//
// 助手来源含**同包生产文件**：收尾助手常是包内导出的收尾函数，不限测试文件。
//
// 深度 ≤2 的理由：收尾助手自己可能再调一个助手（cancel → 等 done → 再等另一个
// 通道）。深度 1 只认直接助手 ⇒ 那种形态会被误判；深度 3 以上则开始跨出
// 「这个用例自己的收尾链」⇒ 判据失去意义。两头都收在 ≤2。
//
// # 等待点为什么不看 goroutine 自己的函数体
//
// go func(){ ... }() 的**函数体内部**的 <-ch 是那个 goroutine 在等**别人**，不是
// 测试侧在等它——把它算成等待点，等于给「起了不等」的写法发免死金牌。故等待点
// 扫描跳过 go 语句自身的函数字面量。
//
// # 已知边界（这条规则照不到的四类形态）
//
// 下面四条是**这条规则的边界**，不是 bug：它判的是「测试侧有没有等」，
// 不等价于「被等的那个 goroutine 一定被等到了」。写在这里，是因为本仓的纪律
// 要求**「门禁绿 ≠ 机制成立」**：判据照不到的形态必须**显式登记**，不能靠
// 「没报过」反推「不存在」。评审与 controller 的独立复核实测出下面四条，
// 共同形态都是**假阴性**——规则照不到，门禁照常绿。
//
//  1. **任意接收表达式都算等待点**——判据认的是「接收表达式 <-ch」这个
//     **形状**，不区分它在等什么。⇒ 一个孤零零的 `<-time.After(time.Second)`
//     单独存在也会被算成「有等待点」⇒ 整个用例被豁免 ⇒ 起的 goroutine
//     实际没被 join，门禁仍报绿。（评审自建夹具 F17 实测为 MISS。）
//
//  2. **豁免粒度是「用例级」，不是「goroutine 级」**——一个用例里只要**有一处**
//     等待点，该用例内**所有** goroutine 一起被豁免，包括那个从未被等的。
//     （代码依据：`gjJudgePackage` 里 `if gjHasWait(tf.decl) { continue }` 的
//     `continue` 位于**用例循环的外层** ⇒ 一次跳过的是整个用例，不是某个
//     goroutine。）
//
//  3. **`X.Wait()` 匹配任意选择器调用**——只要有形如 `X.Wait()` 的调用就算
//     等待点，**与被启动的那个 goroutine 是否相关**不校验。
//     （代码依据：testgoroutine_wait.go 的 `gjHasWaitIn` 里
//     `sel.Sel.Name == "Wait"` 没有关联性检查——一个与本用例无关的
//     `wg.Wait()` 也会给该用例发免死金牌。）
//
//  4. **这是静态形状判据，不是运行时保证**——规则判的是「调用链上**存在**
//     等待点」，**不判「那个等待点一定会执行」**。⇒ 若等待点写成「用例末尾的
//     普通语句」而不是 `defer`，用例中途 `t.Fatalf` 触发
//     `runtime.Goexit` 时那一步**不执行**（实测：Goexit 会执行所有 defer，
//     但**不执行** Goexit 之后的普通语句）⇒ goroutine 实际未被 join，而规则
//     **判它合规**。
//     ✅ **仓内已知的 9 处实例已清偿**（issue #431，v0.62.1）：全部改成 `defer`
//     形态——末尾收尾点写 `defer`；中途收尾点（后面还有下一段）圈成**闭包**、
//     `defer` 写在闭包里（收尾在本段结束时发生，语义不变；失败路径上仍然 join）。
//     ⚠ **但这条边界本身仍在**：规则判不出「那个等待点是不是在 `defer` 内」⇒
//     下一次写「用例末尾的普通语句」照样合规、照样绿。已开单 **issue #440**
//     （把「收尾点必须是 `defer` 形态」变成门禁）。
//
// 边界 4 的措辞别写成「规则有 bug」：它是**规则的边界**——判「有没有等」，
// 不判「等的那一步会不会被跳过」。它与本仓既有纪律「门禁绿 ≠ 机制成立」
// 同族：划定的是规则的**保证到哪儿为止**，不是否定它的价值。
//
// 前三条与第四条的成因不同，别混着读：1~3 是**判据认得比该认的宽**
// （认多了 ⇒ 豁免多了）；4 是**判据认得比该保证的窄**（只认形状，不认执行
// 时序）。两者都让门禁报绿。
//
// # 失败形态
//
// 红（severity: error）。报告必须点名：文件:行、测试函数名、起的 goroutine 形态、
// 以及「测试侧没有等待点」——「判了但看不出是哪条判的」与「没判」在排查时等价。

package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// gjMaxHelperDepth 助手链的最大深度（1 = 只认直接助手）。
//
// 见文件头「为什么豁免必须 helper 感知」：深度 2 认助手的助手，再深就跨出
// 「这个用例自己的收尾链」，豁免面失去意义。
const gjMaxHelperDepth = 2

// check_kind 的取值域与分派在 checkkind.go；本文件是它分派到的那条执行体。

// gjFunc 一条待判定的函数。
type gjFunc struct {
	name string
	decl *ast.FuncDecl
	rel  string // 相对仓库根的路径（报告用）
	fset *token.FileSet
	// timeLocal / runtimeLocal 是本文件里 time 与 runtime 两个 import 的本地名
	// （含别名）。判据按 import 路径认调用，不按源码文本认 ⇒ 别名导入也判得到。
	timeLocal    string
	runtimeLocal string
}

// gjFile 一个已解析的 Go 文件。
type gjFile struct {
	rel  string
	fset *token.FileSet
	file *ast.File
}

// gjPkg 一个包（同目录同包名）的判定面。
//
// 按**包**分组而不是按目录：同目录下可以并存 foo 与 foo_test 两个包，后者
// 看不见前者的未导出符号 ⇒ 把两者混成一个目录索引会让「调用了包内助手」这条
// 豁免跨包生效（假阴性）。
type gjPkg struct {
	pkg     string
	helpers map[string]*gjFunc // 包级函数（不含被judge的测试函数本身）
	tests   []*gjFunc
}

// testGoroutineViolations 是本规则的执行体入口（checkRule 分派到这里）。
//
// 返回 error 而不是「尽力而为的结果」：解析失败意味着那棵子树**没有被判定**，
// 门禁给不出结论时必须响亮失败（见 scan.go 的 iterFiles 头注：门禁给不出结论
// 却显示成通过，是本仓最贵的失效形态）。
func testGoroutineViolations(root string, r Rule) ([]Violation, error) {
	files, err := gjScanFiles(root, r)
	if err != nil {
		return nil, err
	}
	return gjJudgeFiles(files, r.ID), nil
}

// gjScanFiles 收集判定面要的文件（scope ∩ include ∩ scope_exclude）。
//
// **测试文件与生产文件都要收**：被 judge 的只限测试文件（inTestScope），
// 但同包生产文件里的收尾助手是豁免面的合法来源 ⇒ 只收测试文件会把已经修好的
// 用例判成违规（大量误报）。判据面（哪一层的文件参与判定）与豁免面（哪一层的
// 文件可作等待点来源）是两件事，分开收。
func gjScanFiles(root string, r Rule) ([]gjFile, error) {
	var out []gjFile
	for _, scope := range r.Scope {
		p := filepath.Join(root, scope)
		if _, err := os.Stat(p); err != nil {
			// scope 指向不存在的目录由 policy 自校验报（那里能指出该改哪一行），
			// 这里静默跳过——与 ruleFiles 同口径。
			continue
		}
		files, err := iterFiles(p, r.Include, false)
		if err != nil {
			return nil, fmt.Errorf("规则 %s: %w", r.ID, err)
		}
		for _, f := range files {
			rel, _ := filepath.Rel(root, f)
			if gjScopeExcluded(rel, r.ScopeExclude) {
				continue
			}
			fset := token.NewFileSet()
			af, err := parser.ParseFile(fset, f, nil, 0)
			if err != nil {
				return nil, fmt.Errorf("规则 %s: 解析 %s 失败（该文件未被判定）: %w", r.ID, rel, err)
			}
			out = append(out, gjFile{rel: rel, fset: fset, file: af})
		}
	}
	return out, nil
}

// gjScopeExcluded 判文件是否落在 scope_exclude 里（与 scan.go 的 ruleFiles 同口径）。
func gjScopeExcluded(rel string, exclude []string) bool {
	for _, ex := range exclude {
		if rel == ex || strings.HasPrefix(rel, ex+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// gjJudgeFiles 是**唯一的判定核**：把一组文件判成违规清单。
//
// 真判定（testGoroutineViolations）与 policy examples 的加载期自检
// （gjSampleViolations）都走这里——样例与真判定共用同一个核，样例过了才说明
// 真判定也过（两套实现各自漂移，样例那套只看几个字符串、漂得更慢 ⇒ 绿灯更久：
// 「从没见过它触发的规则等于没有规则」最隐蔽的形态）。
func gjJudgeFiles(files []gjFile, ruleID string) []Violation {
	pkgs := gjGroupPackages(files)
	var out []Violation
	for _, p := range pkgs {
		out = append(out, gjJudgePackage(p, ruleID)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// gjGroupPackages 按包（同目录同包名）分组，并索引包级助手与测试函数。
func gjGroupPackages(files []gjFile) []*gjPkg {
	byKey := map[string]*gjPkg{}
	var order []string
	for _, f := range files {
		key := filepath.Dir(f.rel) + "|" + f.file.Name.Name
		p := byKey[key]
		if p == nil {
			p = &gjPkg{pkg: f.file.Name.Name, helpers: map[string]*gjFunc{}}
			byKey[key] = p
			order = append(order, key)
		}
		isTest := strings.HasSuffix(f.rel, "_test.go")
		for _, dec := range f.file.Decls {
			fd, ok := dec.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			fn := &gjFunc{
				name:         fd.Name.Name,
				decl:         fd,
				rel:          f.rel,
				fset:         f.fset,
				timeLocal:    tsImportLocal(f.file, "time"),
				runtimeLocal: tsImportLocal(f.file, "runtime"),
			}
			if isTest && gjIsTestFunc(fd.Name.Name) {
				p.tests = append(p.tests, fn)
				continue
			}
			p.helpers[fd.Name.Name] = fn
		}
	}
	out := make([]*gjPkg, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

// gjIsTestFunc 判函数名是不是一个测试用例。
//
// TestMain 不算：它是整个测试二进制的入口，不是「一个用例」——它起的 goroutine
// 活到进程结束是设计，不是泄漏。把它算进来会让规则在**每个**有 TestMain 的包
// 里报一条与纪律无关的违反。
func gjIsTestFunc(name string) bool {
	if name == "TestMain" {
		return false
	}
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Fuzz")
}

// gjJudgePackage 判一个包里的全部测试函数，返回违规清单。
func gjJudgePackage(p *gjPkg, ruleID string) []Violation {
	var out []Violation
	for _, tf := range p.tests {
		goStmts := gjGoStmts(tf.decl)
		if len(goStmts) == 0 {
			continue
		}
		if gjHasWait(tf.decl) {
			continue
		}
		if gjHasWaitInHelpers(p, tf) {
			continue
		}
		// 一个用例里起多个 goroutine 时**逐条报**：每条违规各自指向自己的
		// go 语句行号，读者不必自己数第几个。只报第一个会让「还漏了几个」
		// 变成读者要自己推断的事——那与「没报」在排查时等价。
		for _, gs := range goStmts {
			pos := tf.fset.Position(gs.Pos())
			out = append(out, Violation{
				Rule: ruleID,
				Path: tf.rel,
				Line: pos.Line,
				Text: fmt.Sprintf(
					"用例 %s 起了 goroutine（%s）但测试侧没有等待点："+
						"函数体内既无 <-ch 接收也无 X.Wait() 调用，同包助手（深度 ≤%d）里也没有"+
						"⇒ goroutine 可能在用例返回后仍在跑，撞上用例交还资源的那道窗口",
					tf.name, gjGoForm(tf.fset, gs), gjMaxHelperDepth),
				Kind: "test_goroutine_join",
			})
		}
	}
	return out
}

// gjGoStmts 取函数体（含嵌套闭包）里的全部 go 语句。
func gjGoStmts(fn *ast.FuncDecl) []*ast.GoStmt {
	var out []*ast.GoStmt
	ast.Inspect(fn, func(x ast.Node) bool {
		if x == nil {
			return false
		}
		if gs, ok := x.(*ast.GoStmt); ok {
			out = append(out, gs)
		}
		return true
	})
	return out
}

// 等待点与助手链的判定在 testgoroutine_wait.go（豁免面）。

// gjGoForm 渲染 go 语句的形态（单行、截断），供报告点名。
func gjGoForm(fset *token.FileSet, gs *ast.GoStmt) string {
	var sb strings.Builder
	if err := printer.Fprint(&sb, fset, gs); err != nil {
		return "go ..."
	}
	form := strings.Join(strings.Fields(sb.String()), " ")
	// 按 rune 截断：按字节切会把多字节字符切成非法 UTF-8（报告里出现乱码，
	// 而报告是门禁的对外输出，乱码等于信息丢失）。
	runes := []rune(form)
	if len(runes) > 80 {
		form = string(runes[:80]) + "…"
	}
	return form
}
