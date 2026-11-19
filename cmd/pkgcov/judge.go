// judge.go —— 事实 + 阈值 + 豁免 → 报告（判据的另一半）。
//
// 这里没有任何 I/O：全程是纯函数，故判据的每条分支都能被表驱动用例直接钉住。
// 把「读日志」与「下判断」分开，是本命令从 awk 改成 Go 的主要理由——awk 版把
// 判定逻辑与 shell 调用面糊在一起，唯一能测的方式是真的跑一次 make check。
package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/oekit/pkg/stringx"
)

// options 判据参数。
type options struct {
	// min 逐包覆盖率下限（百分比数值）。
	min float64
	// exempt 覆盖率豁免：包路径**后缀**匹配（写 `cmd/docsync` 比写全 module 路径可读）。
	exempt []string
	// structuralExempt 结构性豁免：同样按后缀匹配，但**判据语义不同**。
	//
	// exempt 的语义是「覆盖率不达标、但可以用例补上去」的包：豁免包一旦自己
	// 达到下限就判红（豁免失效，当天拆）。
	//
	// structuralExempt 的语义是「**包的性质**决定通用覆盖率判据不适用」
	// （环境依赖型）：豁免包自己达标**不**判红——豁免的理由仍在。
	// 它仍然「失效即红」，但失效的判据是「**没命中任何包**」（包没了/改名了），
	// 不是「自己达标了」。
	structuralExempt []string
	// noTestExempt 零测试文件豁免：同样按后缀匹配。
	noTestExempt []string
}

// report 判定结果。
type report struct {
	// counted 计入判定的包数（有覆盖率且未被豁免）。
	counted int
	// exempted 生效中的覆盖率豁免数。
	exempted int
	// structuralExempted 生效中的结构性豁免数。
	//
	// 它必须**在报告里看得见**：豁免可以有，但不能静默，也不能无期限。
	structuralExempted int
	// noTestDeclared 生效中的零测试豁免数。
	noTestDeclared int
	// below 低于下限的包。
	below []string
	// staleExempt 已失效的覆盖率豁免。
	staleExempt []string
	// staleStructuralExempt 已失效的结构性豁免（没命中任何包）。
	staleStructuralExempt []string
	// inapplicableStructuralExempt 结构性豁免指向的包**形态不适用**
	// （零测试文件 / 无语句）⇒ 豁免对它不生效。
	//
	// 它与 staleStructuralExempt 是**两件事**：后者是「包根本不在日志里」
	// （包没了/改名了），前者是「包**在**日志里、只是形态对不上」。混在一起
	// ⇒ 维护者照着「没有包命中这条结构性豁免」去找一个**就在日志里**的包，
	// 找不到 ⇒ 红得说不出原因（本仓明令要避免的失败形态）。
	inapplicableStructuralExempt []string
	// conflictingExempt 同时登记在两张豁免表里的包（口径冲突）。
	conflictingExempt []string
	// undeclaredNoTest 未声明的零测试包。
	undeclaredNoTest []string
	// staleNoTestExempt 已失效的零测试豁免（该包已有测试，或包不存在）。
	staleNoTestExempt []string
}

// ok 报告是否可以放行。
func (r report) ok() bool {
	return len(r.below) == 0 && len(r.staleExempt) == 0 &&
		len(r.staleStructuralExempt) == 0 && len(r.conflictingExempt) == 0 &&
		len(r.undeclaredNoTest) == 0 && len(r.staleNoTestExempt) == 0 &&
		len(r.inapplicableStructuralExempt) == 0
}

// hasSuffix 按后缀匹配包路径。
//
// 后缀匹配而不是前缀：README 与 Makefile 里写 `cmd/docsync` 可读，而全路径前缀
// （github.com/yi-nology/goagent-kernel）在每条豁免里都是噪音。边界必须对齐「/」
// 或整串相等——否则 `sync` 会匹配到 `cmd/docsync`，那是**放行方向**的漏判。
func hasSuffix(pkg, suffix string) bool {
	if suffix == "" {
		return false
	}
	if pkg == suffix {
		return true
	}
	return strings.HasSuffix(pkg, "/"+suffix)
}

// matchIndex 返回命中的豁免下标（未命中返回 -1）。
func matchIndex(list []string, pkg string) int {
	for i, s := range list {
		if hasSuffix(pkg, s) {
			return i
		}
	}
	return -1
}

// judge 判定（纯函数，用例直接调它，不经过进程）。
func judge(results []pkgResult, o options) report {
	var r report
	hitCoverage := make([]bool, len(o.exempt))
	hitStructural := make([]bool, len(o.structuralExempt))
	hitNoTest := make([]bool, len(o.noTestExempt))

	for _, res := range results {
		// 结构性豁免的命中**先**算出来，且**不论形态**：豁免表登记的是**包**，
		// 「包在不在日志里」与「这个包的形态适不适用」必须分开判。
		//
		// 混在一起的后果（本次修掉的缺陷）：零测试文件 / 无语句的包在下面两个
		// 短路分支里 continue ⇒ 结构性豁免「没命中任何包」⇒ 报错「没有包命中
		// 这条结构性豁免」——可那个包**就在日志里**，维护者照着去找一个**不存在**
		// 的包。红得说不出原因，与常红同样会训练人忽略门禁。
		si := matchIndex(o.structuralExempt, res.pkg)
		if si >= 0 {
			hitStructural[si] = true
		}
		if res.noTestFiles {
			// 零测试文件形态归 PKG_NO_TEST_EXEMPT，**不归**结构性豁免。
			if i := matchIndex(o.noTestExempt, res.pkg); i >= 0 {
				hitNoTest[i] = true
				r.noTestDeclared++
			} else {
				r.undeclaredNoTest = append(r.undeclaredNoTest, res.pkg)
			}
			if si >= 0 {
				r.inapplicableStructuralExempt = append(r.inapplicableStructuralExempt, fmt.Sprintf(
					"%s: 该包没有任何测试文件 ⇒ 零测试文件形态归 PKG_NO_TEST_EXEMPT，"+
						"不归结构性豁免 ⇒ 请从 PKG_COV_STRUCTURAL_EXEMPT 移除", res.pkg))
			}
			continue
		}
		if !res.hasCoverage {
			// `[no statements]`：不适用，既不计入分母也不判违规。
			// 纯接口包**不进任何豁免**（既有行为）。
			if si >= 0 {
				r.inapplicableStructuralExempt = append(r.inapplicableStructuralExempt, fmt.Sprintf(
					"%s: 该包没有任何可统计语句（[no statements]）⇒ 纯接口包不进任何豁免 "+
						"⇒ 请从 PKG_COV_STRUCTURAL_EXEMPT 移除", res.pkg))
			}
			continue
		}
		// 两张豁免表各自命中哪一个，先算出来。
		//
		// 同包命中两张是**口径冲突**：同一个包既被说成「还缺用例」又被说成
		// 「性质决定判据不适用」⇒ 必须报出来，别默默取一个——默默取一个等于
		// 判据自己藏起一处矛盾，下一个人读到的是一份自相矛盾的声明面。
		ci := matchIndex(o.exempt, res.pkg)
		if ci >= 0 && si >= 0 {
			hitCoverage[ci] = true
			// 冲突分支也要计入计数桶：不记 ⇒ 报告里的「N 个结构性豁免」少报，
			// 而「豁免不能静默」正是这条纪律的意图。**每个被豁免的包只数一次**
			// ⇒ 这里计入结构性豁免桶，不重复计入普通豁免桶。
			r.structuralExempted++
			r.conflictingExempt = append(r.conflictingExempt, fmt.Sprintf(
				"%s: 同时登记在 PKG_COV_EXEMPT（%s）与 PKG_COV_STRUCTURAL_EXEMPT（%s）",
				res.pkg, o.exempt[ci], o.structuralExempt[si]))
			continue
		}
		if si >= 0 {
			// 结构性豁免：**不受**「自己达标即红」约束。
			//
			// 那条判据问的是「豁免的理由还在吗」。环境依赖是**包的性质**，
			// 不是「用例还没写」——本机碰巧有 PostgreSQL 时覆盖率上去了，
			// 豁免的理由一个字都没变，故这里不判 stale。
			//
			// 它仍然是「失效即红」，只是失效判据换成「没命中任何包」
			// （包被删了/改名了 ⇒ 豁免的对象没了 ⇒ 条目该拆）。
			r.structuralExempted++
			continue
		}
		if ci >= 0 {
			hitCoverage[ci] = true
			if res.coverage >= o.min {
				r.staleExempt = append(r.staleExempt,
					fmt.Sprintf("%s: 已达 %.1f%%（下限 %.0f%%）", res.pkg, res.coverage, o.min))
				continue
			}
			r.exempted++
			continue
		}
		r.counted++
		if res.coverage < o.min {
			r.below = append(r.below, fmt.Sprintf("%s: %.1f%%", res.pkg, res.coverage))
		}
	}

	// 失效判定：豁免写了但没命中任何包（包改名/被删/本来就有测试）。
	for i, s := range o.exempt {
		if !hitCoverage[i] {
			r.staleExempt = append(r.staleExempt, fmt.Sprintf("%s: 没有包命中这条豁免", s))
		}
	}
	for i, s := range o.structuralExempt {
		if !hitStructural[i] {
			r.staleStructuralExempt = append(r.staleStructuralExempt,
				fmt.Sprintf("%s: 没有包命中这条结构性豁免", s))
		}
	}
	for i, s := range o.noTestExempt {
		if !hitNoTest[i] {
			r.staleNoTestExempt = append(r.staleNoTestExempt,
				fmt.Sprintf("%s: 该包已有测试文件或不存在，豁免该拆了", s))
		}
	}
	return r
}

// String 渲染报告（成功一行、失败分条）。
func (r report) String(o options) string {
	if r.ok() {
		return fmt.Sprintf("逐包覆盖率下限 %.0f%% ✓（%d 个包计入，%d 个覆盖率豁免，%d 个结构性豁免，%d 个零测试豁免）\n",
			o.min, r.counted, r.exempted, r.structuralExempted, r.noTestDeclared)
	}
	var b strings.Builder
	if len(r.below) > 0 {
		fmt.Fprintf(&b, "以下包低于逐包下限 %.0f%%：\n", o.min)
		for _, s := range r.below {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		b.WriteString("修法：补该包的用例（阈值是真阈值，不要靠调低 PKG_COV_MIN 变绿）。\n")
	}
	if len(r.undeclaredNoTest) > 0 {
		b.WriteString("以下包没有任何测试文件，且未声明豁免：\n")
		for _, s := range r.undeclaredNoTest {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		b.WriteString("修法：补一个冒烟用例；确属夹具/装配层的，登记到 Makefile 的 PKG_NO_TEST_EXEMPT 并写清理由。\n")
	}
	if len(r.staleExempt) > 0 {
		b.WriteString("逐包下限豁免已失效（必须当天拆除，不能静默留着）：\n")
		for _, s := range r.staleExempt {
			fmt.Fprintf(&b, "  - %s ⇒ 请从 PKG_COV_EXEMPT 移除\n", s)
		}
	}
	if len(r.staleStructuralExempt) > 0 {
		b.WriteString("结构性豁免已失效（包没了/改名了 ⇒ 必须当天拆除）：\n")
		for _, s := range r.staleStructuralExempt {
			fmt.Fprintf(&b, "  - %s ⇒ 请从 PKG_COV_STRUCTURAL_EXEMPT 移除\n", s)
		}
	}
	if len(r.inapplicableStructuralExempt) > 0 {
		b.WriteString("结构性豁免指向的包形态不适用（包就在日志里，但不是它该豁免的形态）：\n")
		for _, s := range r.inapplicableStructuralExempt {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		b.WriteString("修法：零测试文件形态登记到 PKG_NO_TEST_EXEMPT；纯接口包（无语句）不进任何豁免。\n")
	}
	if len(r.conflictingExempt) > 0 {
		b.WriteString("同一个包同时登记在两张豁免表里（口径冲突，必须拆掉一条）：\n")
		for _, s := range r.conflictingExempt {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		b.WriteString("修法：同一个包只能登记在一张表里——「还缺用例」的类型登记进 PKG_COV_EXEMPT；\n")
		b.WriteString("「性质决定通用覆盖率判据不适用」的类型登记进 PKG_COV_STRUCTURAL_EXEMPT。\n")
	}
	if len(r.staleNoTestExempt) > 0 {
		b.WriteString("零测试豁免已失效（必须当天拆除）：\n")
		for _, s := range r.staleNoTestExempt {
			fmt.Fprintf(&b, "  - %s ⇒ 请从 PKG_NO_TEST_EXEMPT 移除\n", s)
		}
	}
	return b.String()
}

// splitList 拆逗号分隔的豁免表（去空白、丢空项）。
func splitList(s string) []string {
	// 拆逗号 + 去空白 + 丢空项是上游 stringx.SplitCSVNonEmpty 的逐字语义；
	// 排序是本函数自己的要求（清单出口一律有序，免得两次输出两种顺序）。
	out := stringx.SplitCSVNonEmpty(s)
	slices.Sort(out)
	return out
}
