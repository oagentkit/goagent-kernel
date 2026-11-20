package roundguard_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// ── 导出 API 面上限门禁（抄 smhanov/agentloop 的 apibudget_test.go，2026-11-21）──
//
// # 这道门禁防的是什么
//
// 导出面随实现自由生长 ⇒「宿主能依赖什么」失去边界 ⇒ 边界一丢，下游升级成本
// 无上限。判据包一旦开始长胖，宿主就会开始依赖那些「其实只是实现细节」的符号，
// 于是它再也删不掉。故「加一个公开 API」必须是**一次自觉行为**（一次带理由的
// 变更），而不是「顺手加个导出方法」的副产品。
//
// # 三条判据（缺一不可）
//
//  1. **天花板**：导出面超过上限 ⇒ 红。上限是「整张脸长到多大就该停下来复审」
//     的总闸。
//  2. **精确计数**：当前导出面必须**恰好等于**登记值。任何对导出面的增删、或对
//     计数规则的改动，都会让这个数字变化 ⇒ 用例变红。
//     **为什么必须有这条**：只断言「当前数 ≤ 天花板」的门禁**分不出**「导出面
//     没变大」与「计数逻辑被放松了」——把方法计数去掉、把 _test.go 放回计数面、
//     把天花板抬到 999，测试照样全绿。精确计数才是防「悄悄放松」的那道闸。
//  3. **计数规则自检**：证明「导出方法被计入」「_test.go 不计入」——否则前两条
//     钉住的是**一个被放松过的口径**下的数字。
//
// # 计数规则（什么算、什么不算）
//
// 门禁数的是**交付面**：宿主 import 这个包之后能拿到的名字。
//
// 计入：
//   - 包级导出函数（无接收者）各记 1；
//   - 包级导出类型各记 1；
//   - 导出**结构体**的导出具名字段各记 1；
//   - 导出**结构体**的导出嵌入字段各记 1（嵌入的导出类型名本身成为本包的导出名）；
//   - 导出**结构体**经嵌入**提升**上来的导出方法各记 1（未导出类型被嵌入时，它的
//     导出方法被提升进导出类型 ⇒ 宿主能通过导出类型调用它 ⇒ 属交付面）；
//   - 导出**接口**的导出方法集各记 1（给导出接口加一个方法 ⇒ 宿主实现方被迫多
//     实现一个 ⇒ 破坏性变更）；
//   - 导出**接口**经嵌入提升上来的导出方法各记 1；
//   - 包级导出 const / var 按 spec 逐名各记 1；
//   - **导出方法**（方法名导出，且**接收者类型名导出**）各记 1——含指针接收者。
//
// 不计入：
//   - _test.go 文件里的任何符号（测试函数不是交付面：宿主 import 不到它们）；
//   - 未导出的符号（名字首字母非大写）；
//   - **未导出类型上、且未被提升的方法**（宿主拿不到那个类型，也调不到那个方法）；
//   - 非结构体底层类型的字段（type X []Y / type X = Y ⇒ 0 个字段）；
//   - **嵌入的导出类型自己的方法**（已在它自己的声明处计过 ⇒ 不重复计）。
//
// ⚠️ **为什么「导出方法」必须计入**：给已有类型加一个导出方法，是**最容易悄悄
// 扩大 API 面**的动作（不动任何类型声明、不动任何调用点），而只数无接收者的
// 计数口径对那类改动**完全失明**。
//
// ⚠️ **为什么「接口的方法集」与「嵌入提升的方法」也必须计入**：它们是另外两条
//   **同样容易、同样隐蔽**的增长路径——给导出接口加一个方法会**破坏**宿主的实现
//   （实现方被迫多实现一个）；给被嵌入的未导出类型加一个导出方法，会让宿主多出
//   一个可调用方法。两条都不新增任何**类型**声明 ⇒ 只按「声明」数的口径对它们
//   一律失明 ⇒ 「精确计数」会在一条增长路径上彻底失效。
//
// ⚠️ **测量面只含本包目录**（roundguard/）下的生产文件：**子目录不在测量面内**
//   ——将来若出现 roundguard/xxx 子包，它的导出符号对这道门禁**不可见**（那时要
//   另设门禁或把子包纳入）。当前本包无子包，规则口径也只承诺「本包」。
//
// ⚠️ **为什么 _test.go 必须排除**：测试函数名首字母大写 = 导出符号，若一并计入，
// 同一个包的「导出面」会被测试代码撑大一倍（实测 104 → 193），而那不是交付面。
//
// 计数引擎（怎么数）在本包的 exportbudget_count_test.go——本文件只管**判**。

const (
	// exportedAPICeiling 导出面**总闸**（本包导出符号总数的上限）。
	//
	// 天花板 = **实测值 + 余量**：
	//
	//	实测：104（2026-11-21，按上面「计数规则」口径实测本包生产文件）
	//	天花板：136
	//	余量：32
	//
	// **余量为什么是 32**：用**本包自己的实测面成本**算，不拍脑袋——门禁自己会
	// 打出逐文件计数：budget.go=29 / detect.go=27 / decide.go=12 / classify.go=12
	// ⇒ 本包历史上**一次完整判据面扩展**的交付面成本最大是 **29**（budget 那次：
	// 三档额度 + 用量 + 三个 Check* 谓词 + 进程内回合账）。余量 32 = 29（一次完整
	// 扩展）+ 3（缓冲）⇒ **够一次完整的最大规模扩展**，不必为了加一个判据面而
	// 把好 API 删掉。
	//
	// ⚠️ **为什么不取更紧的余量**：这道门禁的失败文案是「删导出面，不是抬天花板」。
	// 若余量小于一次扩展的成本（如 16 < 29），它会**主动把维护者推向删一次自觉、
	// 合理的公开 API** ⇒ 「假红的门禁逼人删好 API」与「常红的门禁等于没有门禁」
	// 是同一类病。故余量按**本包实测的最大一次扩展成本 + 缓冲**取，宁可松一点。
	//
	// **余量用完（计数触到 136）该走什么流程**：**删导出面**（把实现细节收进
	// 包内、或合并同类结论），**不是抬天花板**。抬天花板必须是**单独一次变更**
	// 并附理由（与「加导出符号」同一条纪律），且要连带复核下面的精确计数——
	// 两件事混在一次提交里，审阅者就看不出哪一笔是「这次新加的」。
	exportedAPICeiling = 136

	// exportedAPICount 当前导出面的**精确计数**（实测值，随面变化而更新）。
	//
	// 改这个数字 = 承认「导出面变了」⇒ 必须是一次自觉行为（附理由）。
	// 它与 exportedAPICeiling 是两道不同的闸：天花板管「总共能长多大」，
	// 这个数管「**每一次**增删都要被看见」。
	exportedAPICount = 104
)

// TestExportedAPISurfaceStaysUnderCeiling 判据 1：天花板（超限即红）。
func TestExportedAPISurfaceStaysUnderCeiling(t *testing.T) {
	total, _ := exportedSurface(t, ".")
	if total > exportedAPICeiling {
		t.Fatalf("导出面 %d 个符号，超过天花板 %d。\n"+
			"加公开 API 必须是一次自觉行为（单独一次变更 + 理由）；"+
			"天花板用完时的正确处置是**删导出面**，不是抬天花板。",
			total, exportedAPICeiling)
	}
}

// TestExportedAPISurfaceMatchesRecordedCount 判据 2：精确计数（防「悄悄放松」）。
//
// **为什么必须有这条**：只断言「当前数 ≤ 天花板」的门禁分不出「导出面没变大」
// 与「计数逻辑被放松了」——把方法计数去掉、把 _test.go 放回计数面、把天花板抬到
// 999，测试照样全绿。这条用例把「当前的数」钉成一个具体数字：任何对导出面的
// 增删、或对计数规则的改动，都会让这个数字变化 ⇒ 用例变红。
func TestExportedAPISurfaceMatchesRecordedCount(t *testing.T) {
	total, perFile := exportedSurface(t, ".")
	if total != exportedAPICount {
		t.Fatalf("导出面实测 %d，与登记值 %d 不符（逐文件：%s）。\n"+
			"两种可能，处理方式不同：\n"+
			"  - 「加了导出符号」：连同理由一起改 exportedAPICount（一次自觉行为）；\n"+
			"  - 「计数规则被改了」：先改规则，并复核 TestCountingRules* 那几条自检用例。",
			total, exportedAPICount, formatPerFile(perFile))
	}
}

// TestCountingRulesCountExportedMethods 判据 3a：**导出方法被计入**。
//
// 做法是「同一段源码，加方法前后各数一次」：差值必须**恰好**是导出方法的个数。
// 若有人把方法计数从口径里去掉，两个数字就相等 ⇒ 差值 0 ≠ 2 ⇒ 用例变红。
// 未导出的方法（hidden）不得计入 ⇒ 差值是 2 而不是 3。
func TestCountingRulesCountExportedMethods(t *testing.T) {
	const withMethods = `package sample

type Widget struct {
	ID   string
	size int
}

func (w Widget) Describe() string { return w.ID }
func (w *Widget) Reset()            { w.size = 0 }
func (w Widget) hidden()            {}

func Build() Widget { return Widget{} }
func helper() int   { return 0 }
`
	const withoutMethods = `package sample

type Widget struct {
	ID   string
	size int
}

func Build() Widget { return Widget{} }
func helper() int   { return 0 }
`
	withN := countSource(t, withMethods)
	withoutN := countSource(t, withoutMethods)
	// 基线：type Widget(1) + 导出字段 ID(1) + 包级导出函数 Build(1) = 3。
	if withoutN != 3 {
		t.Fatalf("计数基线不对：未加方法的样例数出 %d（应为 3）——计数规则被改了？", withoutN)
	}
	// 差值 = 两个导出方法（Describe / Reset）；未导出方法（hidden）不计入。
	if withN-withoutN != 2 {
		t.Fatalf("导出方法未被计入：加方法的样例数出 %d，未加的 %d，差值 %d（应为 2）——"+
			"给已有类型加一个导出方法是最容易悄悄扩大 API 面的动作，它必须在计数面里",
			withN, withoutN, withN-withoutN)
	}
}

// TestCountingRulesExcludeTestFiles 判据 3b：**_test.go 不计入**。
//
// 门禁数的是**交付面**（宿主 import 得到的符号），测试函数不是交付面。
// 若 _test.go 被放回计数面，同一个包的导出面会被测试代码撑大一倍，而门禁从此
// 变成「测试代码量」的看门狗 ⇒ 真正的导出面变化被淹没。
func TestCountingRulesExcludeTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prod.go"), "package sample\n\nfunc Exported() {}\n")
	writeFile(t, filepath.Join(dir, "prod_test.go"),
		"package sample\n\nfunc ExportedToo() {}\n\nfunc TestSomething(t *testing.T) {}\n")
	total, _ := exportedSurface(t, dir)
	if total != 1 {
		t.Fatalf("导出面计数 %d（应为 1）：_test.go 不属于交付面，"+
			"测试函数名首字母大写也不该计入", total)
	}
}

// TestCountingRulesCountInterfaceMethods 判据 3d：**导出接口的方法集被计入**。
//
// 给导出接口**加一个方法**，宿主侧的实现方就被迫多实现一个 ⇒ 破坏性变更。
// 而「加方法」只多一条**方法声明**——按「声明」数的口径对这类改动**完全失明**
// ⇒ 「精确计数」会在一条增长路径上彻底失效。
func TestCountingRulesCountInterfaceMethods(t *testing.T) {
	const oneMethod = `package sample

type Probe interface {
	Alpha() int
}
`
	const twoMethods = `package sample

type Probe interface {
	Alpha() int
	Beta() string
}
`
	one := countSource(t, oneMethod)
	two := countSource(t, twoMethods)
	// 基线：type Probe(1) + 方法 Alpha(1) = 2。
	if one != 2 {
		t.Fatalf("计数基线不对：一个导出方法的接口数出 %d（应为 2）——接口的方法集没被计入？", one)
	}
	if two-one != 1 {
		t.Fatalf("给导出接口加一个方法，计数只变了 %d（应为 1）——"+
			"给接口加方法是破坏性变更（宿主实现方被迫多实现一个），它必须在计数面里",
			two-one)
	}
}

// TestCountingRulesCountPromotedMethods 判据 3e：**嵌入提升上来的导出方法被计入**。
//
// 未导出类型被导出类型嵌入时，它的导出方法被**提升**进导出类型 ⇒ 宿主能通过
// 导出类型调用它（Outer.Promoted()）⇒ 属交付面。只数「嵌入字段名」的话，这类
// 方法对计数**完全不可见**。
//
// 同时钉住**不重复计**：嵌入的若是**导出**类型，它的方法已在它自己的声明处计过
// ⇒ 不能再在嵌入处计一次。
func TestCountingRulesCountPromotedMethods(t *testing.T) {
	const withPromoted = `package sample

type inner struct{}

func (inner) Promoted() int { return 0 }

type Outer struct {
	inner
}
`
	const withoutPromoted = `package sample

type inner struct{}

type Outer struct {
	inner
}
`
	withN := countSource(t, withPromoted)
	withoutN := countSource(t, withoutPromoted)
	// 基线：type Outer(1) + 嵌入的 inner（未导出 ⇒ 0）= 1。
	if withoutN != 1 {
		t.Fatalf("计数基线不对：未加方法的样例数出 %d（应为 1）", withoutN)
	}
	if withN-withoutN != 1 {
		t.Fatalf("嵌入提升上来的导出方法未被计入：加方法的样例 %d，未加的 %d，差值 %d（应为 1）——"+
			"宿主能通过 Outer.Promoted() 调用它，它属交付面", withN, withoutN, withN-withoutN)
	}
	// 不重复计：嵌入**导出**类型时，它的方法已在它自己的声明处计过。
	const exportedInner = `package sample

type Inner struct{}

func (Inner) Promoted() int { return 0 }

type Outer struct {
	Inner
}
`
	// Inner(1) + Promoted(1) + Outer(1) + 嵌入的 Inner(1) = 4。
	if got := countSource(t, exportedInner); got != 4 {
		t.Fatalf("嵌入导出类型时计数 %d（应为 4）——提升上来的方法被**重复计**了", got)
	}
}

// TestProductionFilesAreNotInExternalTestPackage 判据 3c：生产文件不得声明成
// 外部测试包（roundguard_test）。
//
// 判据走 **go/ast 的包名**而不是源码文本：生产文件里出现「package roundguard_test」
// 这串**文本**可能是注释或字符串里的内容，逐行找文本会误判；而包名写错是**结构
// 性**事实，只有语法树说得准。
func TestProductionFilesAreNotInExternalTestPackage(t *testing.T) {
	files, err := productionGoFiles(".")
	if err != nil {
		t.Fatalf("列生产文件失败：%v", err)
	}
	for _, path := range files {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("解析 %s 失败：%v", path, perr)
		}
		if f.Name.Name != "roundguard" {
			t.Fatalf("%s 的包名是 %q，与目录不符——生产文件不得声明成外部测试包", path, f.Name.Name)
		}
	}
}
