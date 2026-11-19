package main

import (
	"strings"
	"testing"
)

// judge_shape_test.go —— 结构性豁免的**形态**判据与**计数**（M3 / M4）。
//
// # 为什么单独一个文件
//
// judge_structural_test.go 钉的是「两张豁免表的分派语义」（谁归谁、谁判红）；
// 本文件钉的是另外两件事：豁免**指向的包形态对不上**时怎么说、豁免数**怎么数**。
// 两者关注面不同，且本文件的每条用例都要带一条「坏实现会红」的对照断言——
// 那些对照断言在原文件里会被淹没（它按表驱动跑，红绿对读起来像同一条）。
//
// # 合成日志的形状（见 parse_test.go 的 syntheticLog）
//
//	alpha     88.0%  有覆盖率、达标
//	beta      64.5%  有覆盖率、不达标
//	contract  —      [no statements]（纯接口包，无语句）
//	cmd/tool  —      无测试文件（-coverprofile 形态）
//	cmd/fixture / cmd/plain — 无测试文件
//
// 故「结构性豁免指向形态不适用的包」有两种可合成形态：
// cmd/tool（无测试文件）与 contract（无语句）。

// TestJudgeStructuralExemptShapeMismatch 锁死 M3：结构性豁免指向的包
// **形态不适用**时，报的是形态问题，不是「没有包命中这条结构性豁免」。
//
// # 为什么这条用例承重
//
// 旧实现里零测试文件 / 无语句两个短路分支在豁免匹配**之前** continue ⇒ 结构性
// 豁免永远作用不到这两类包 ⇒ 报告说「没有包命中这条结构性豁免」——而那个包
// **就在日志里**。维护者照着文案去找一个**不存在的包**，于是红得说不出原因。
//
// # 「坏实现会红」的对照（本仓纪律：只测「好实现能过」的用例不承重）
//
//   - 把形态分支去掉（回到旧行为）⇒ inapplicableStructuralExempt 为空 ⇒ 红；
//   - 把形态分支留着但文案改回旧措辞 ⇒ 断言「报告不含『没有包命中』」⇒ 红；
//   - 把 hitStructural 的赋值挪回覆盖率分支之后 ⇒ staleStructuralExempt 非空 ⇒ 红。
func TestJudgeStructuralExemptShapeMismatch(t *testing.T) {
	cases := []struct {
		name string
		// structural 结构性豁免表（登记一个形态不适用的包）。
		structural []string
		// wantPkg 期望被点名「形态不适用」的包。
		wantPkg string
		// wantHint 文案里必须出现的形态说明（说清是形态问题、怎么办）。
		wantHint string
	}{
		{
			name:       "结构性豁免指向无测试文件的包 ⇒ 红且文案说形态问题",
			structural: []string{"cmd/tool"},
			wantPkg:    "example.com/m/cmd/tool",
			wantHint:   "没有任何测试文件",
		},
		{
			name:       "结构性豁免指向无语句的包 ⇒ 红且文案说形态问题",
			structural: []string{"contract"},
			wantPkg:    "example.com/m/contract",
			wantHint:   "没有任何可统计语句",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := options{
				min:              70,
				exempt:           []string{"beta"},
				structuralExempt: c.structural,
				// cmd/tool 故意**不**进零测试豁免：它要红在「形态不适用」，
				// 而不是红在「零测试包未声明」——否则断言会红在别处，钉不住。
				noTestExempt: []string{"cmd/fixture", "cmd/plain"},
			}
			r := judge(parseLog(t), o)
			if r.ok() {
				t.Fatalf("形态不适用必须红（不许变绿），实际报告：%s", r.String(o))
			}
			if len(r.inapplicableStructuralExempt) != 1 {
				t.Fatalf("应有 1 条「形态不适用」，实际 %d 条（报告：%s）",
					len(r.inapplicableStructuralExempt), r.String(o))
			}
			msg := r.inapplicableStructuralExempt[0]
			if !strings.Contains(msg, c.wantPkg) {
				t.Errorf("形态不适用那条应点名那个包（维护者靠它定位），实际：%q", msg)
			}
			if !strings.Contains(msg, c.wantHint) {
				t.Errorf("文案应说清形态问题（%q），实际：%q", c.wantHint, msg)
			}
			if !strings.Contains(msg, "PKG_COV_STRUCTURAL_EXEMPT") {
				t.Errorf("文案应指名从哪张表移除，实际：%q", msg)
			}
			// 关键断言：**不**说「没有包命中」——包就在日志里。
			if len(r.staleStructuralExempt) != 0 {
				t.Errorf("包在日志里（只是形态不适用）⇒ 不该报「没有包命中这条结构性豁免」，实际：%v",
					r.staleStructuralExempt)
			}
			s := r.String(o)
			if strings.Contains(s, "没有包命中这条结构性豁免") {
				t.Errorf("报告文案不该说「没有包命中」（包就在日志里），实际：\n%s", s)
			}
			if !strings.Contains(s, "形态不适用") {
				t.Errorf("报告应有专属的形态小节，实际：\n%s", s)
			}
		})
	}
}

// TestJudgeStructuralExemptConflictCountedOnce 锁死 M4：冲突分支的包也进计数桶，
// 且**每个被豁免的包只数一次**。
//
// # 为什么这条用例承重
//
// 冲突分支原先直接 continue：既不 r.exempted++ 也不 r.structuralExempted++，
// r.counted 同样不含它 ⇒ 报告里的「N 个结构性豁免」在冲突场景下少报。
// 豁免不能静默——少报一次计数，就是让「豁免被默默吃掉」这件事没有信号。
//
// # 「坏实现会红」的对照
//
//   - 把冲突分支的 r.structuralExempted++ 去掉 ⇒ 计数少 1 ⇒ 红；
//   - 在冲突分支里**两个**桶都 ++ ⇒ 同一个包被数两次 ⇒ 红。
func TestJudgeStructuralExemptConflictCountedOnce(t *testing.T) {
	o := options{
		min:              70,
		exempt:           []string{"alpha", "beta"},
		structuralExempt: []string{"alpha"},
		noTestExempt:     noTestExemptAll,
	}
	r := judge(parseLog(t), o)
	if r.ok() {
		t.Fatalf("同包命中两张表必须红，实际报告：%s", r.String(o))
	}
	if len(r.conflictingExempt) != 1 {
		t.Fatalf("应有 1 条口径冲突，实际 %d 条（报告：%s）",
			len(r.conflictingExempt), r.String(o))
	}
	// alpha 同时命中两张表 ⇒ 它计入结构性豁免计数。
	if r.structuralExempted != 1 {
		t.Errorf("结构性豁免计数应含冲突的那个包 = 1，实际 %d", r.structuralExempted)
	}
	// 每个被豁免的包只数一次：alpha（冲突）+ beta（普通豁免）= 2 个包。
	// 两个桶都 ++ 冲突的包 ⇒ 这里是 3 ⇒ 红。
	if r.exempted+r.structuralExempted != 2 {
		t.Errorf("被豁免的包数应为 2（alpha 冲突 + beta 普通豁免），实际 %d（exempted=%d, structuralExempted=%d）",
			r.exempted+r.structuralExempted, r.exempted, r.structuralExempted)
	}
	// 豁免掉的包不进「计入判定」的桶（它豁免了，不是被下限判了）：
	// alpha 冲突（豁免）、beta 普通豁免 ⇒ 两个包都被豁免 ⇒ counted 应为 0。
	if r.counted != 0 {
		t.Errorf("豁免掉的包不该进 counted（alpha 冲突 + beta 普通豁免 ⇒ 应为 0），实际 %d", r.counted)
	}
}
