// wallclock_classify.go —— 墙钟判定点的**分类对账**（issue #481）。
//
// # 这层判什么
//
// 检测层（wallclock_threshold.go）只回答「这里有没有一处墙钟阈值判定」；
// 这一层回答的是 issue 要的那句话：「这个量变了我判的东西还成立吗」的**答案**。
//
// 答案不能留在脑子里 ⇒ 逐条写进 policy 的 `ruler_classifications`，并由门禁对账。
// 三个方向都要判（缺一条，这条判据就是半个）：
//
//  1. **漏登记**：检测到的判定点没有对应登记 ⇒ 红（未判过的结论不许进仓）。
//  2. **登记成尺子类** ⇒ 红（尺子类必须改写成可观测事实；登记只是记账，不是放行）。
//  3. **僵尸登记**：登记了但代码里已无该形态 ⇒ 红（改完要删登记，否则清单会烂）。
//
// # 这层为什么不能省
//
// 「超时上限类」与「尺子类」在语法上**分不开**：同一条 `elapsed > 15s` 在一条
// 用例里是宽松上限、在另一条里是紧阈值，差别只在「坏实现会耗时多久」，那不在
// 语法里 ⇒ 判据推不出来 ⇒ 只能逐条判定并写下来。
//
// 而「写下来」如果没有机器盯着，下一次改代码时它就会过期、失效、悄悄变成装饰
// ⇒ 这层就是那个机器。
//
// # 同一键下的对应关系
//
// 键 = 目录 + 函数名 + 形态。同一键下**多处**判定点按出现顺序与登记项一一
// 对应：登记少一条即漏登记，多一条即僵尸登记。
package archcheck

import (
	"fmt"
	"sort"
	"strings"
)

// ── 形态与分类的取值域（封闭；拼错即红，见 policy 自校验）────────────────

// wcFormFixedSeconds 「固定秒数断言」：把已流逝时长与**固定时长常量**比较。
const wcFormFixedSeconds = "fixed_seconds_assert"

// wcFormTimerOnlySelect 「纯计时器窗口」：select 里**只有**计时器分支。
//
// 带事实分支的 select（等一个事件/一个信号）**不是**这一形态：那种窗口是
// 「等事实的上限」，结论由事实决定（超时上限类）。
const wcFormTimerOnlySelect = "timer_only_select"

// wcClassTimeoutCap 超时上限类：窗口只作等待上界，结论由等到的**事实**决定。
const wcClassTimeoutCap = "timeout_cap"

// wcClassRuler 尺子类：结论由窗口长度单独决定 ⇒ 必须改写成可观测事实。
const wcClassRuler = "ruler"

// wcFormNames 列出已知的墙钟判定形态（自校验与报错文案用）。
func wcFormNames() []string {
	return []string{wcFormFixedSeconds, wcFormTimerOnlySelect}
}

// wcClassNames 列出已知的墙钟判定分类（自校验与报错文案用）。
func wcClassNames() []string {
	return []string{wcClassTimeoutCap, wcClassRuler}
}

// wcKnownForm 判一个形态名是否已知。
func wcKnownForm(form string) bool {
	for _, f := range wcFormNames() {
		if f == form {
			return true
		}
	}
	return false
}

// wcKnownClass 判一个分类名是否已知。
func wcKnownClass(class string) bool {
	for _, c := range wcClassNames() {
		if c == class {
			return true
		}
	}
	return false
}

// ── 对账 ──────────────────────────────────────────────────────────────

// wcClassifySites 把检测到的墙钟判定点与 policy 的分类登记对账。
func wcClassifySites(r Rule, sites []wcSite) []Violation {
	groups := map[wcClassKey][]wcSite{}
	var order []wcClassKey
	for _, s := range sites {
		k := wcClassKey{s.pkgDir, s.funcName, s.form}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], s)
	}
	regs := map[wcClassKey][]RulerClassification{}
	var regOrder []wcClassKey
	for _, c := range r.RulerClassifications {
		k := wcClassKey{c.Package, c.Func, c.Form}
		if _, ok := regs[k]; !ok {
			regOrder = append(regOrder, k)
		}
		regs[k] = append(regs[k], c)
	}

	var out []Violation
	for _, k := range order {
		got := groups[k]
		entries := regs[k]
		for i, s := range got {
			if i >= len(entries) {
				out = append(out, wcUnclassifiedViolation(r, s, k))
				continue
			}
			c := entries[i]
			switch {
			case c.Class == wcClassRuler:
				out = append(out, wcRulerClassViolation(r, s, c))
			case !wcKnownClass(c.Class):
				out = append(out, wcUnknownClassViolation(r, s, c))
			}
		}
	}
	// 僵尸登记：登记了、但判定面里已找不到该形态。
	for _, k := range regOrder {
		if len(groups[k]) > 0 {
			continue
		}
		for _, c := range regs[k] {
			out = append(out, wcStaleClassViolation(r, k, c))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// ── 渲染 ──────────────────────────────────────────────────────────────

// wcSiteViolation 渲染一条「检测到的墙钟判定点」。
//
// 样例层用它（样例判的是「识别」）；真仓里它不直接判红——真仓走分类对账。
func wcSiteViolation(r Rule, s wcSite) Violation {
	return Violation{
		Rule: r.ID,
		Path: s.rel,
		Line: s.line,
		Text: fmt.Sprintf(
			"%s 有一处墙钟阈值判定（%s，形态 %s）：%s\n"+
				"  墙钟阈值要么改写成可观测事实（尺子类），要么显式登记为「超时上限类」"+
				"并写明理由——它不该默默留在用例里。",
			s.rel, s.funcName, s.form, s.detail),
		Kind: "test_wallclock_threshold",
	}
}

// wcUnclassifiedViolation 渲染一条「未分类的墙钟判定」。
func wcUnclassifiedViolation(r Rule, s wcSite, k wcClassKey) Violation {
	return Violation{
		Rule: r.ID,
		Path: s.rel,
		Line: s.line,
		Text: fmt.Sprintf(
			"%s 有一处**未分类的墙钟判定**（%s，形态 %s）：%s\n"+
				"  墙钟阈值必须逐条给出结论——要么改写成可观测事实（尺子类），"+
				"要么显式登记为「超时上限类」并写明理由（policy 的 ruler_classifications，"+
				"键 %s）。\n"+
				"  漏登记不是「还没判」，是「没人判」——它让这一处继续随机器快慢漂。",
			s.rel, s.funcName, s.form, s.detail, k),
		Kind: "test_wallclock_unclassified",
	}
}

// wcRulerClassViolation 渲染一条「登记为尺子类的墙钟判定」。
func wcRulerClassViolation(r Rule, s wcSite, c RulerClassification) Violation {
	return Violation{
		Rule: r.ID,
		Path: s.rel,
		Line: s.line,
		Text: fmt.Sprintf(
			"%s 的一处墙钟判定被登记为**尺子类**（%s，形态 %s）：%s\n"+
				"  尺子类的结论由窗口长度单独决定 ⇒ 机器一忙就假绿/假红 ⇒ 必须改写成"+
				"可观测事实（通道、状态、计数器这类能被读出来的事实）。\n"+
				"  登记理由：%s\n"+
				"  登记只是记账，不是放行：尺子类登记后仍判红，改完请把这条登记删掉。",
			s.rel, s.funcName, s.form, s.detail, c.Reason),
		Kind: "test_wallclock_ruler_class",
	}
}

// wcUnknownClassViolation 渲染一条「分类名未知」的登记。
//
// 正常路径下 policy 自校验先报它；这里仍判红，是为了让**执行体自身**不依赖
// 自校验先跑（自校验恒开，但判据不该把「别人先跑过」当前提）。
func wcUnknownClassViolation(r Rule, s wcSite, c RulerClassification) Violation {
	return Violation{
		Rule: r.ID,
		Path: s.rel,
		Line: s.line,
		Text: fmt.Sprintf(
			"%s 的一处墙钟判定登记了未知分类 %q（已知取值：%s）——"+
				"取值域必须封闭：拼错的分类名会让这条登记静默失效（门禁变绿）。",
			s.rel, c.Class, strings.Join(wcClassNames(), " / ")),
		Kind: "test_wallclock_unknown_class",
	}
}

// wcStaleClassViolation 渲染一条「僵尸登记」。
func wcStaleClassViolation(r Rule, k wcClassKey, c RulerClassification) Violation {
	return Violation{
		Rule: r.ID,
		Path: "architecture-policy.yaml",
		Text: fmt.Sprintf(
			"墙钟判定分类登记 %s 已找不到对应形态（僵尸登记）——"+
				"要么那一处已改写成可观测事实（那就把这条登记删掉），"+
				"要么它挪了位置（那就把登记的键改对）。留着不删，清单会烂成装饰。",
			k),
		Kind: "test_wallclock_stale_class",
	}
}
