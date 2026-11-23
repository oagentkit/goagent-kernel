// wallclock.go —— 「断言的尺子必须是可观测事实，不是墙钟窗口」的判定体。
//
// # 这条规则判什么
//
// issue #441：测试面不许拿**裸 time.Sleep** 当尺子。
//
// 固定睡一段再断言 ⇒ 这段窗口与被测机制的真实节奏无关：机器一忙，窗口里
// 可能什么都没发生（假红）；被测机制抢跑，断言又可能落在窗口之外（白测）。
// 两种失效形态的共同点是：**结论由窗口长度单独决定**，而不是由可观测事实
// 决定。
//
// # 判定口径（逐条）
//
//  1. 判定面：**只扫测试文件**（policy only_test: true）、**只判测试函数**
//     （Test / Benchmark / Fuzz 开头的包级函数；TestMain 不在面内）。
//  2. 尺子形态：time.Sleep(...) 调用出现在测试函数体里（或它调用的**包内
//     助手**里），且**不在任何 for/range 循环体内** ⇒ 违规。
//  3. **轮询退避不算尺子**：sleep 落在 for/range 循环体内 ⇒ 不判。它的退出
//     条件是循环条件本身（可观测事实），不是窗口长度。
//     ⇒ 这是本规则**显式登记的边界**，写在这里与报告里，不靠「没报过」反推。
//  4. **goroutine 体里的 sleep 不判**：那是**被测机制自己的节奏**
//     （起一个 goroutine 干 30ms 的活），不是测试侧的尺子。判据面因此排除
//     go 语句自身的函数字面量（与兄弟规则同口径）。
//  5. **受管辅助**：policy 侧声明的「等可观测事实辅助」（managed_helpers），
//     它们函数体内部的 sleep 不判——这些助手已被单独复核过「等的是事实，
//     不是窗口」。
//
// # 为什么判据走 go/ast，不走正则
//
// import 有别名形态、调用有选择器/裸标识符形态、循环有 for/range 两种形态、
// go 语句的函数字面量要单独跳过——逐行正则必然漏，且漏得毫无声响。
// 故本文件全程走语法树（见本仓纪律「结构性的判定走 go/ast」）。
//
// # 报告必须点名
//
// 文件:行、sleep 的形态、为什么判成「尺子」（窗口长度单独决定结论），
// 以及它落在哪儿（用例体 / 哪个助手）。
package archcheck

import (
	"fmt"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// wallclockRulerViolations 是本规则的执行体入口。
//
// **两层**（issue #481）：
//
//	① 检测层：裸 sleep 形态（wcJudgeFiles）+ 墙钟阈值形态（wcThresholdSites）；
//	② 对账层：阈值形态与 policy 的分类登记对账（wcClassifySites）。
//
// 为什么阈值形态不直接判红而要对账：「超时上限类」（窗口只作等待上界）与
// 「尺子类」（结论由窗口长度单独决定）在语法上**分不开**——同一条
// `elapsed > 15s` 在一条用例里是宽松上限、在另一条里是紧阈值，差别只在
// 「坏实现会耗时多久」，那不在语法里 ⇒ 只能逐条判定并写下来，再由门禁盯着
// 那张表（漏登记即红 / 登记成尺子类即红 / 改完不删登记即红）。
func wallclockRulerViolations(root string, r Rule) ([]Violation, error) {
	files, err := gjScanFiles(root, r)
	if err != nil {
		return nil, err
	}
	out := wcJudgeFiles(files, r)
	out = append(out, wcClassifySites(r, wcThresholdSites(files, r))...)
	wcSortViolations(out)
	return out, nil
}

// wcSortViolations 按「路径 → 行号」排违规清单（两层的输出合并后要重排）。
func wcSortViolations(out []Violation) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
}

// wcJudgeFiles 是本规则的**唯一判定核**（真判定与 policy 样例共用）。
func wcJudgeFiles(files []gjFile, r Rule) []Violation {
	pkgs := gjGroupPackages(files)
	var out []Violation
	for _, p := range pkgs {
		out = append(out, wcJudgePackage(p, r)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// wcJudgePackage 判一个包里的全部测试函数。
func wcJudgePackage(p *gjPkg, r Rule) []Violation {
	var out []Violation
	seen := map[string]bool{}
	for _, tf := range p.tests {
		b := tsNewBuilder(tf.decl, tf.fset, tf.timeLocal, tf.runtimeLocal)
		scopes := tsAllScopes(b.build())
		// ① 测试函数体里的 sleep。
		for _, s := range scopes {
			for _, sl := range s.sleeps {
				if sl.inLoop {
					continue
				}
				out = append(out, wcViolation(r, tf.rel, sl, "用例 "+tf.name+" 的函数体里"))
			}
		}
		// ② 被测试函数调用的包内助手里的 sleep（助手也是尺子的载体）：
		// 助手链深度 ≤2（与兄弟规则同款——助手自己可能再调一个助手）。
		for _, s := range scopes {
			locals := tsLocalNames(s.root)
			for _, c := range s.calls {
				if locals[c.name] {
					continue
				}
				out = append(out, wcHelperChainSleeps(p, r, c.name, seen)...)
			}
		}
	}
	return out
}

// wcHelperChainSleeps 收助手链（深度 ≤2）里的墙钟 sleep。
//
// 受管的辅助（policy managed_helpers）不判：它们函数体内部的 sleep 已被
// 单独复核过「不是尺子」。受管助手**仍然继续往下走**——它的被调方是另一个
// 助手，该不该判由那个助手自己的登记决定，不靠上游助手连带豁免。
func wcHelperChainSleeps(p *gjPkg, r Rule, name string, seen map[string]bool) []Violation {
	var out []Violation
	frontier := []string{name}
	for depth := 0; depth < gjMaxHelperDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, cur := range frontier {
			h, ok := p.helpers[cur]
			if !ok || seen[h.name] {
				continue
			}
			seen[h.name] = true
			if !wcManaged(r, p.pkg, h.name) {
				out = append(out, wcHelperSleeps(p, r, h)...)
			}
			next = append(next, gjCalleeNames(h.decl)...)
		}
		frontier = next
	}
	return out
}

// wcHelperSleeps 收一个助手函数体里的墙钟 sleep（不含轮询退避）。
func wcHelperSleeps(p *gjPkg, r Rule, h *gjFunc) []Violation {
	var out []Violation
	b := tsNewBuilder(h.decl, h.fset, h.timeLocal, h.runtimeLocal)
	for _, s := range tsAllScopes(b.build()) {
		for _, sl := range s.sleeps {
			if sl.inLoop {
				continue
			}
			out = append(out, wcViolation(r, h.rel, sl, "包内助手 "+h.name+" 的函数体里"))
		}
	}
	return out
}

// wcViolation 渲染一条墙钟尺子违规。
func wcViolation(r Rule, rel string, sl tsSleep, where string) Violation {
	return Violation{
		Rule: r.ID,
		Path: rel,
		Line: sl.line,
		Text: fmt.Sprintf(
			"%s有一处裸 time.Sleep（第 %d 行 %s）是**墙钟尺子**："+
				"它是一段固定窗口，窗口长度单独决定结论——机器一忙，窗口里可能什么都没发生（假红）；"+
				"被测机制抢跑，证据就落空（白测）⇒ 断言的尺子必须是可观测事实"+
				"（通道、状态、计数器这类能被读出来的事实），不是墙钟窗口；"+
				"轮询退避（sleep 落在 for/range 循环体内）不算尺子，本判定不报它",
			where, sl.line, sl.form),
		Kind: "test_wallclock_ruler",
	}
}

// wcManaged 判一个助手是不是 policy 侧登记的「受管的等可观测事实辅助」。
//
// 登记形态可以是裸名（waitTail）或带包名的限定名（eventbus.waitTail）。
// 判定面是**同一个包**里的助手（与兄弟规则同口径：只认包级函数，方法不在面内）。
func wcManaged(r Rule, pkgName, helperName string) bool {
	for _, m := range r.ManagedHelpers {
		if m == helperName {
			return true
		}
		if m == pkgName+"."+helperName {
			return true
		}
	}
	return false
}

// wcSampleViolations 用**同一个判定核**判一条 policy 样例。
//
// 走 wcJudgeFiles（与真判定同一份代码）⇒ 样例过了才说明真判定也过：
// 两套实现各自漂移，样例那套只看几个字符串、漂得更慢 ⇒ 绿灯更久，
// 正是「从没见过它触发的规则等于没有规则」最隐蔽的形态。
func wcSampleViolations(r Rule, text string) ([]Violation, error) {
	files, err := tsSampleFiles(r, text)
	if err != nil {
		return nil, err
	}
	out := wcJudgeFiles(files, r)
	// 墙钟阈值形态：样例判的是**识别**（这个形状会不会被认成墙钟判定点），
	// 不判分类对账——样例没有仓内登记可对，那一层由 Go 用例钉住。
	for _, s := range wcThresholdSites(files, r) {
		out = append(out, wcSiteViolation(r, s))
	}
	wcSortViolations(out)
	return out, nil
}

// dcSampleViolations 用**同一个判定核**判一条 policy 样例（defer_cleanup 规则）。
func dcSampleViolations(r Rule, text string) ([]Violation, error) {
	files, err := tsSampleFiles(r, text)
	if err != nil {
		return nil, err
	}
	return dcJudgeFiles(files, r.ID), nil
}

// tsSampleFiles 把一段样例源码解析成判定面（package 行可省）。
func tsSampleFiles(r Rule, text string) ([]gjFile, error) {
	src := text
	if !tsHasPackageClause(src) {
		src = "package sample\n\n" + src
	}
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, sampleFileName(r), src, 0)
	if err != nil {
		return nil, fmt.Errorf("规则 %s 的样例不是可解析的 Go 源码: %w", r.ID, err)
	}
	return []gjFile{{rel: sampleFileName(r), fset: fset, file: af}}, nil
}

// tsHasPackageClause 判样例源码有没有 package 行。
func tsHasPackageClause(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, "package ")
	}
	return false
}
