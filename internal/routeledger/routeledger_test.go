package routeledger

import (
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/internal/ledgerstatus"
)

// goodLedger 一份合规的台账视图：#261 / #262 已合入，#279（总纲单）仍开着。
func goodLedger() *ledgerstatus.Ledger {
	return ledgerstatus.ParseText(strings.Join([]string{
		"## 9. issue 台账",
		"",
		"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
		"|---|---|---|---|---|",
		"| #261 | L1 的单 | L1 | 判据 | ✅ 已合入 (#331) |",
		"| #262 | L2 的单 | L2 | 判据 | ✅ 已合入 (#331) |",
		"| #279 | 总纲单 | 全程 | 判据 | 🔴 仍开着 |",
		"",
	}, "\n"), "test.md")
}

// goodRegistry 一份合规的路线登记面。
func goodRegistry() *Registry {
	return &Registry{
		Updated:       "2026-11-24",
		OverviewIssue: 279,
		Stages: []Stage{{
			ID:           "L1",
			Title:        "身份与租约闭环",
			Issues:       []int{261, 262},
			ExitCriteria: "动作身份唯一；超 TTL 后旧持有者的提交被拒绝",
		}},
	}
}

// problemsText 把问题拼成一段（报告里要能读）。
func problemsText(r Result) string {
	return strings.Join(r.Problems, "\n")
}

// 正面路径：登记齐、成员都在台账里已合入 ⇒ 无问题。
func TestJudge_合规登记全绿(t *testing.T) {
	res := Judge(goodRegistry(), goodLedger())
	if len(res.Problems) != 0 {
		t.Fatalf("合规登记不该有违反，实际：%s", problemsText(res))
	}
	if res.Stages != 1 || res.Members != 2 || res.Achieved != 2 {
		t.Fatalf("计数不对：阶段 %d / 成员 %d / 已达成 %d", res.Stages, res.Members, res.Achieved)
	}
}

// 判据 2：成员在台账里仍开着 ⇒ 红，并点名阶段与单号。
func TestJudge_成员仍开着即红并点名(t *testing.T) {
	led := ledgerstatus.ParseText(strings.Join([]string{
		"| 单号 | 标题 | 状态 |",
		"|---|---|---|",
		"| #261 | L1 的单 | ✅ 已合入 (#331) |",
		"| #262 | L2 的单 | 🔴 仍开着 |",
		"| #279 | 总纲单 | 🔴 仍开着 |",
		"",
	}, "\n"), "test.md")
	res := Judge(goodRegistry(), led)
	if len(res.Problems) == 0 {
		t.Fatal("成员仍开着时必须判红（路线未达成）")
	}
	if !strings.Contains(problemsText(res), "L1") || !strings.Contains(problemsText(res), "#262") {
		t.Fatalf("问题说明应点名阶段与单号，实际：%s", problemsText(res))
	}
	if !strings.Contains(problemsText(res), "仍开着") {
		t.Fatalf("问题说明应说出台账里的实际状态，实际：%s", problemsText(res))
	}
}

// 判据 2：成员在台账登记面里缺席 ⇒ 红。
func TestJudge_成员未登记即红(t *testing.T) {
	led := ledgerstatus.ParseText(strings.Join([]string{
		"| 单号 | 标题 | 状态 |",
		"|---|---|---|",
		"| #261 | L1 的单 | ✅ 已合入 (#331) |",
		"| #279 | 总纲单 | 🔴 仍开着 |",
		"",
	}, "\n"), "test.md")
	res := Judge(goodRegistry(), led)
	if len(res.Problems) == 0 {
		t.Fatal("成员在台账里缺席时必须判红")
	}
	if !strings.Contains(problemsText(res), "没有登记") {
		t.Fatalf("问题说明应点明未登记，实际：%s", problemsText(res))
	}
}

// 判据 3：总纲单在台账里消失 ⇒ 红。
func TestJudge_总纲单未登记即红(t *testing.T) {
	led := ledgerstatus.ParseText(strings.Join([]string{
		"| 单号 | 标题 | 状态 |",
		"|---|---|---|",
		"| #261 | L1 的单 | ✅ 已合入 (#331) |",
		"| #262 | L2 的单 | ✅ 已合入 (#331) |",
		"",
	}, "\n"), "test.md")
	res := Judge(goodRegistry(), led)
	if len(res.Problems) == 0 {
		t.Fatal("总纲单从台账消失时必须判红")
	}
	if !strings.Contains(problemsText(res), "总纲单") {
		t.Fatalf("问题说明应点明总纲单，实际：%s", problemsText(res))
	}
}

// 判据 1：阶段 id 重复 ⇒ 红。
func TestJudge_阶段id重复即红(t *testing.T) {
	reg := goodRegistry()
	reg.Stages = append(reg.Stages, reg.Stages[0])
	res := Judge(reg, goodLedger())
	if len(res.Problems) == 0 {
		t.Fatal("阶段 id 重复必须判红")
	}
}

// 判据 1：成员为空 ⇒ 红（一个没有成员的阶段不是阶段）。
func TestJudge_成员为空即红(t *testing.T) {
	reg := goodRegistry()
	reg.Stages[0].Issues = nil
	res := Judge(reg, goodLedger())
	if len(res.Problems) == 0 {
		t.Fatal("空成员阶段必须判红")
	}
}

// 判据 1：出口条件为空 ⇒ 红（阶段没有可判的完成口径 ⇒ 「已达成」没人能核）。
func TestJudge_出口条件为空即红(t *testing.T) {
	reg := goodRegistry()
	reg.Stages[0].ExitCriteria = ""
	res := Judge(reg, goodLedger())
	if len(res.Problems) == 0 {
		t.Fatal("没有出口条件的阶段必须判红")
	}
}

// 判据 1：updated 非法 ⇒ 红。
func TestJudge_Updated非法即红(t *testing.T) {
	reg := goodRegistry()
	reg.Updated = "2026/11/24"
	res := Judge(reg, goodLedger())
	if len(res.Problems) == 0 {
		t.Fatal("updated 非法时必须判红")
	}
}

// 判据 1：同一阶段内成员重复 ⇒ 红。
func TestJudge_成员重复即红(t *testing.T) {
	reg := goodRegistry()
	reg.Stages[0].Issues = []int{261, 261}
	res := Judge(reg, goodLedger())
	if len(res.Problems) == 0 {
		t.Fatal("同一阶段内成员重复必须判红")
	}
}

// 判据 1：总纲单未声明 ⇒ 红。
func TestJudge_总纲单未声明即红(t *testing.T) {
	reg := goodRegistry()
	reg.OverviewIssue = 0
	res := Judge(reg, goodLedger())
	if len(res.Problems) == 0 {
		t.Fatal("没声明总纲单时必须判红")
	}
}

// 判据跑不起来（登记面或台账缺失）⇒ 给不出结论，不能当成「没有违反」。
func TestJudge_判据跑不起来即红(t *testing.T) {
	res := Judge(nil, goodLedger())
	if len(res.Problems) == 0 {
		t.Fatal("登记面缺失时必须报出（跑不起来的门禁等于没有门禁）")
	}
}
