// testnocall.go —— 「测试面不得直接调用包级全局态 setter」的判定体（issue #449）。
//
// # 这条规则判什么
//
// 包级全局态（SetEnabled / SetSurfaces / …）被用例**直调**时，还原约定只能靠
// 每个用例作者自己记着写 cleanup——两套相反的还原约定（一个还原成 false、
// 另一个还原成 true）会长在同一个包里，且**没有任何判据会红**：它们各自都
// 「看起来对」，只有 `-count>1` 才会让它现形（本仓 #442 实测 20 次红 19 次）。
//
// 收进**一个**包级 helper 之后，还原口径只有一份；本规则守的是「别绕开它」——
// 下一个用例作者仍可以直接写 SetEnabled(false) + 自己的 cleanup，那条纪律
// 目前只是纪律（#449 的原话：两套还原约定可以原样再生）。
//
// # 判定口径（逐条）
//
//  1. 判定面：**只扫测试文件**（policy only_test: true），判**调用点**
//     （go/ast），不判源码文本——注释里、字符串里的 SetEnabled 不算调用。
//  2. 被禁符号由 **policy 声明**（forbid_calls）：archcheck 是跨宿主复用的库，
//     「哪些是全局态 setter」是宿主的知识，硬编码等于把宿主的事写进内核。
//  3. **受管辅助**（managed_helpers）体内的调用不判：helper 自己必须调真 setter，
//     否则它就没法成为唯一的还原入口。
//  4. 未登记的包内助手体内的调用**照判**：助手是绕开 helper 的第二条路。
//
// # 为什么走 go/ast
//
// 要判的是「调用点」这个语法事实：同名的函数定义、注释、字符串、测试名
// （TestSetDigestKeyGuards）都不是调用。逐行正则必然把它们一起命中，
// 于是规则要么误报到没人信它、要么被人加豁免到形同虚设。
package archcheck

import (
	"fmt"
	"sort"
	"strings"
)

// testNoDirectCallViolations 是本规则的执行体入口。
func testNoDirectCallViolations(root string, r Rule) ([]Violation, error) {
	files, err := gjScanFiles(root, r)
	if err != nil {
		return nil, err
	}
	return tncJudgeFiles(files, r), nil
}

// tncJudgeFiles 是本规则的**唯一判定核**（真判定与 policy 样例共用）。
//
// 样例走它 ⇒ 样例过了才说明真判定也过：两套实现各自漂移，样例那套只看几个
// 字符串、漂得更慢 ⇒ 绿灯更久，正是「从没见过它触发的规则等于没有规则」最
// 隐蔽的形态。
func tncJudgeFiles(files []gjFile, r Rule) []Violation {
	forbidden := map[string]bool{}
	for _, c := range r.ForbidCalls {
		if c = strings.TrimSpace(c); c != "" {
			forbidden[c] = true
		}
	}
	if len(forbidden) == 0 {
		// 没有声明被禁符号 ⇒ 本规则没有判定面。这由 policy 自校验报红
		// （check_kind 已知但缺 forbid_calls），不在这里静默返回零违规。
		return nil
	}
	var out []Violation
	for _, p := range gjGroupPackages(files) {
		out = append(out, tncJudgePackage(p, r, forbidden)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// tncJudgePackage 判一个包：测试函数体 + 未登记为受管的包内助手。
func tncJudgePackage(p *gjPkg, r Rule, forbidden map[string]bool) []Violation {
	var out []Violation
	// ① 用例体里的直调——issue #449 点名的形态。
	for _, tf := range p.tests {
		out = append(out, tncScanFunc(r, tf, p.pkg, forbidden, "用例 "+tf.name+" 的函数体里")...)
	}
	// ② 包内助手体里的直调：助手是绕开 helper 的第二条路。
	//    已登记为受管辅助的（policy managed_helpers）不判——helper 自己必须调真 setter。
	for _, h := range p.helpers {
		if tncManaged(r, p.pkg, h.name) {
			continue
		}
		out = append(out, tncScanFunc(r, h, p.pkg, forbidden,
			"包内助手 "+h.name+" 的函数体里（未登记为受管辅助）")...)
	}
	return out
}

// tncScanFunc 扫一个函数体里的全部调用点。
func tncScanFunc(r Rule, f *gjFunc, pkgName string, forbidden map[string]bool, where string) []Violation {
	var out []Violation
	b := tsNewBuilder(f.decl, f.fset, f.timeLocal, f.runtimeLocal)
	for _, s := range tsAllScopes(b.build()) {
		for _, c := range s.calls {
			if !forbidden[c.name] {
				continue
			}
			out = append(out, tncViolation(r, f.rel, c, where))
		}
	}
	return out
}

// tncManaged 判一个助手是否被 policy 登记为受管辅助（与 wallclock 同款口径：
// 裸名或带包名的限定名都认）。
func tncManaged(r Rule, pkgName, helperName string) bool {
	for _, m := range r.ManagedHelpers {
		if m == helperName || m == pkgName+"."+helperName {
			return true
		}
	}
	return false
}

// tncViolation 渲染一条直调违规。
func tncViolation(r Rule, rel string, c tsCall, where string) Violation {
	return Violation{
		Rule: r.ID,
		Path: rel,
		Line: c.line,
		Text: fmt.Sprintf(
			"%s有一处对 %s 的直接调用（第 %d 行）：包级全局态的还原约定只应有"+
				"**一份**（收进包级 helper），用例直调等于让每个作者各写一遍 cleanup——"+
				"两套相反的还原约定会长在同一个包里，且没有任何判据会红，只有 -count>1 才现形"+
				"（issue #442 实测 20 次红 19 次）⇒ 改走包级 helper；确需直调的在 policy 的"+
				"managed_helpers 里登记它并写明拆除条件",
			where, c.name, c.line),
		Kind: "test_no_direct_call",
	}
}

// tncSampleViolations 用**同一个判定核**判一条 policy 样例。
func tncSampleViolations(r Rule, text string) ([]Violation, error) {
	files, err := tsSampleFiles(r, text)
	if err != nil {
		return nil, err
	}
	return tncJudgeFiles(files, r), nil
}
