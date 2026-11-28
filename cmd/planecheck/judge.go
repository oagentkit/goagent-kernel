// judge.go —— docs/PLANES.md 两张表的解析与跨面依赖判定（判据纯函数）。
//
// # 这条判据补的是什么缺口
//
// 评审（2026-11-27 §3.3 问题四）：控制面/执行面/恢复面概念上必须分清——不拆仓，
// 但「新包归哪面、跨面哪个方向合法」要有判据，不靠 review 感觉。此前包间依赖
// 只有 dependency_direction 的 requires（「登记过没有」），没有「该不该」的平面
// 维度；执行链反向长出调度逻辑、控制面直接操纵恢复链这类形状，没有任何门禁会拦。
//
// # 三条判据（失败形态是变红）
//
//  1. **映射覆盖**：包→平面映射覆盖 api-lifecycle 全集（双向），平面值在
//     C/E/R/X（或复合 `C+E` 形态，主平面取首字），职责格非空；
//  2. **规则形状**：跨面规则 from/to/裁决（允许|禁止）/理由全非空，from/to 不得为 X
//     （横切面不参与跨面规则）；
//  3. **边判定**：生产代码里观测到的跨面边（主平面不同、任一侧 X 则跳过）必须在
//     规则表登记——未登记方向对门禁隐形，与 dependency_direction「未登记包」同向；
//     登记为「禁止」却被真实 import 命中 ⇒ 红。预防性禁令（当前零观测）登记即生效。
//
// # 判据与数据源分离
//
// 判定是纯函数（登记面 + 注入的边集合 ⇒ 结论）；md 解析与 import 扫描（go/ast）
// 由 cmd/planecheck 注入 ⇒ 用例可确定性测、不联网。
package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 平面值域。
const (
	planeControl   = "C"
	planeExecution = "E"
	planeRecovery  = "R"
	planeCross     = "X"
)

// 裁决值域。
const (
	verdictAllow  = "允许"
	verdictForbid = "禁止"
)

// updatedRe 抓版本戳行。
var updatedRe = regexp.MustCompile(`(?m)^updated:\s*(\S+)\s*$`)

// 表头（逐字定位）。
const (
	mapTableHeader  = "| pkg | 平面 | 职责 |"
	ruleTableHeader = "| from | to | 裁决 | 理由 |"
)

// planeRe 平面值：单字或复合（C+E），字母不重复。
var planeRe = regexp.MustCompile(`^(?:[CERX])(?:\+[CERX])*$`)

// MapRow 映射表一行。
type MapRow struct {
	Pkg   string
	Plane string
	Duty  string
}

// RuleRow 跨面规则一行。
type RuleRow struct {
	From    string
	To      string
	Verdict string
	Reason  string
}

// Registry 解析后的登记面。
type Registry struct {
	Updated string
	Mapping []MapRow
	Rules   []RuleRow
}

// Edge 一条生产 import 边（A 包 → B 包，两侧都是登记包）。
type Edge struct {
	From string
	To   string
}

// Facts 仓内事实（调用方注入）。
type Facts struct {
	Registered map[string]bool // api-lifecycle 全集
	Edges      []Edge          // 生产代码 import 边（两侧均在全集、去重）
}

// Result 一次判定的结论。
type Result struct {
	Problems []string
	Mapping  int
	Rules    int
	Cross    int // 观测到的跨面边数
}

// Judge 判登记面与边集合是否一致（纯函数）。
func Judge(reg *Registry, f Facts) Result {
	var res Result
	if reg == nil || f.Registered == nil {
		res.Problems = append(res.Problems,
			"三平面登记面或仓内事实缺失：判据跑不起来（跑不起来的门禁等于没有门禁）")
		return res
	}
	res.Mapping = len(reg.Mapping)
	res.Rules = len(reg.Rules)

	res.Problems = append(res.Problems, checkShape(reg)...)

	// 覆盖：映射 ↔ 全集双向。
	seen := map[string]string{}
	for _, m := range reg.Mapping {
		seen[m.Pkg] = m.Plane
		if !f.Registered[m.Pkg] {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"映射表里的包 %s 不在 api-lifecycle 登记面里：平面归属必须挂在生命周期清单上，不许自造包名", m.Pkg))
		}
	}
	for pkg := range f.Registered {
		if _, ok := seen[pkg]; !ok {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"api-lifecycle 登记的包 %s 没有平面归属：漏一个包 = 那个包的跨面依赖对门禁隐形", pkg))
		}
	}

	// 规则索引：from→to → rule。
	rules := map[string]RuleRow{}
	for _, r := range reg.Rules {
		rules[r.From+"→"+r.To] = r
	}

	// 边判定。
	for _, e := range f.Edges {
		pa, okA := seen[e.From]
		pb, okB := seen[e.To]
		if !okA || !okB {
			continue // 未登记包的边由覆盖判据报，这里不重复
		}
		ma, mb := primary(pa), primary(pb)
		if ma == mb || ma == planeCross || mb == planeCross {
			continue // 同面或横切参与的边不算跨面
		}
		res.Cross++
		r, ok := rules[ma+"→"+mb]
		if !ok {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"跨面方向 %s→%s 未登记（观测到 %s → %s 的真实 import）：未登记方向对门禁隐形——先判它该允许还是禁止，再落规则表",
				ma, mb, e.From, e.To))
			continue
		}
		if r.Verdict == verdictForbid {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"禁止方向被命中：%s → %s（%s→%s 规则：禁止）——%s",
				e.From, e.To, ma, mb, r.Reason))
		}
	}
	return res
}

// checkShape 判登记面自身的结构。
func checkShape(reg *Registry) []string {
	var problems []string
	if _, err := time.Parse("2006-01-02", reg.Updated); err != nil {
		problems = append(problems, fmt.Sprintf(
			"三平面登记面的 Updated %q 不是 YYYY-MM-DD：登记面自己的版本戳都不可信", reg.Updated))
	}
	if len(reg.Mapping) == 0 {
		problems = append(problems, "包→平面映射一行都没有（表头下没有数据行）")
	}
	seenPkg := map[string]bool{}
	for _, m := range reg.Mapping {
		if seenPkg[m.Pkg] {
			problems = append(problems, fmt.Sprintf("包 %s 重复映射：同一包两条归属，判到哪条无从定位", m.Pkg))
		}
		seenPkg[m.Pkg] = true
		if !planeRe.MatchString(m.Plane) {
			problems = append(problems, fmt.Sprintf(
				"包 %s 的平面 %q 不在值域（C/E/R/X 或复合 C+E 形态）：平面是归属承诺，值域外的取值读不懂", m.Pkg, m.Plane))
		}
		if strings.TrimSpace(m.Duty) == "" {
			problems = append(problems, fmt.Sprintf("包 %s 的职责格为空：一句话职责是归类可复核的底线", m.Pkg))
		}
	}
	if len(reg.Rules) == 0 {
		problems = append(problems, "跨面规则表一行都没有：没有规则表，跨面依赖退回「靠 review 感觉」")
	}
	seenRule := map[string]bool{}
	for _, r := range reg.Rules {
		key := r.From + "→" + r.To
		if seenRule[key] {
			problems = append(problems, fmt.Sprintf("跨面规则 %s 重复：同一方向两条裁决，听哪条无从定位", key))
		}
		seenRule[key] = true
		for _, p := range []string{r.From, r.To} {
			if !planeRe.MatchString(p) || strings.Contains(p, "+") {
				problems = append(problems, fmt.Sprintf(
					"跨面规则 %s 的平面 %q 不合法：规则按主平面判定，只许单字 C/E/R/X（复合与 X 不参与）", key, p))
			}
			if p == planeCross {
				problems = append(problems, fmt.Sprintf(
					"跨面规则 %s 含 X：横切面不参与跨面规则（谁 import 它都不算跨面）", key))
			}
		}
		if r.Verdict != verdictAllow && r.Verdict != verdictForbid {
			problems = append(problems, fmt.Sprintf(
				"跨面规则 %s 的裁决 %q 不在（允许/禁止）里：裁决只有两态，含糊的取值等于没裁决", key, r.Verdict))
		}
		if strings.TrimSpace(r.Reason) == "" {
			problems = append(problems, fmt.Sprintf("跨面规则 %s 没有理由：方向裁决必须可复核", key))
		}
	}
	return problems
}

// primary 取主平面（复合值首字；异常值原样返回交由值域判据报）。
func primary(plane string) string {
	if i := strings.IndexByte(plane, '+'); i >= 0 {
		return plane[:i]
	}
	return plane
}
