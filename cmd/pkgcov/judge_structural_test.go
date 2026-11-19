package main

import (
	"bytes"
	"strings"
	"testing"
)

// 结构性豁免的用例组：两张豁免表的**分派语义**。
//
// # 为什么单独一个文件
//
// judge_test.go 已经 141 行；本组用例必须成对出现（每条「绿」旁边必有一条
// 「红」），加在一起会把它顶过 400 行（max_file_lines 是真阈值）。故拆文件。
//
// # 为什么每条「绿」旁边必有一条「红」
//
// 本仓纪律：改任何门禁判定逻辑，必须同时给「坏实现会红」的用例。只测好实现
// 的用例，一个把「结构性豁免也判 stale」的错实现照样全过——而那正是本次修掉
// 的缺陷本身。故本文件里的红用例不是凑数，是**给缺陷本身上锁**。
//
// 合成日志的形状（见 parse_test.go 的 syntheticLog）：alpha 88.0%（达标）、
// beta 64.5%（不达标）、contract [no statements]（不适用）、cmd/tool 与
// cmd/fixture 与 cmd/plain 无测试文件（须进零测试豁免，否则红在别处）。

// noTestExemptAll 三个零测试包的豁免（否则本组用例会红在 undeclaredNoTest，
// 而那不是本组要断言的那一栏）。
var noTestExemptAll = []string{"cmd/tool", "cmd/fixture", "cmd/plain"}

// TestJudgeStructuralExempt 覆盖两张豁免表的分派语义。
//
// 断言**红在哪一栏**：只断言「不 ok」的用例挡不住「把结构性豁免记进 staleExempt」
// 这类错位——报告仍是非零退出，但修法提示指向了错误的地方。
func TestJudgeStructuralExempt(t *testing.T) {
	cases := []struct {
		name string
		// exempt 普通豁免表；structural 结构性豁免表。
		exempt     []string
		structural []string
		wantOK     bool
		// below / staleCov / staleStru / conflict 断言红在哪一栏。
		below     int
		staleCov  int
		staleStru int
		conflict  int
		// structCount 生效中的结构性豁免数（绿报告里必须看得见）。
		structCount int
		// inapplicable 形态不适用的结构性豁免数（见 judge_shape_test.go）。
		inapplicable int
	}{
		{
			// 正向：结构性豁免命中且覆盖率达标 ⇒ 绿。
			// 这一条是本次修复的**核心**：alpha 88.0% ≥ 70%，旧逻辑判红
			// 「豁免失效」（本机碰巧有 PostgreSQL），新逻辑判绿（理由仍在）。
			name:        "结构性豁免命中且覆盖率达标 ⇒ 绿（豁免的理由仍在，不是失效）",
			exempt:      []string{"beta"},
			structural:  []string{"alpha"},
			wantOK:      true,
			structCount: 1,
		},
		{
			// 正向：结构性豁免命中但覆盖率不达标 ⇒ 绿。
			// 它本来就不受下限约束——这条锁死「不是把下限放松了」。
			name:        "结构性豁免命中但覆盖率不达标 ⇒ 绿（它本来就不受下限约束）",
			structural:  []string{"beta"},
			wantOK:      true,
			structCount: 1,
		},
		{
			// 反向：结构性豁免没命中任何包 ⇒ 红。
			// 失效检测仍然有牙齿：包没了/改名了 ⇒ 条目该拆。
			name:       "结构性豁免没命中任何包 ⇒ 红在 staleStru（失效检测仍有牙齿）",
			exempt:     []string{"beta"},
			structural: []string{"gone"},
			wantOK:     false,
			staleStru:  1,
		},
		{
			// 反向：普通豁免命中且覆盖率达标 ⇒ 仍红。
			// 别把现有判据改松了——两类豁免的分派不能反过来。
			name:     "普通豁免命中且覆盖率达标 ⇒ 仍红在 staleCov（别把判据改松了）",
			exempt:   []string{"alpha", "beta"},
			wantOK:   false,
			staleCov: 1,
		},
		{
			// 反向：同包同时在两张表 ⇒ 红。
			// 口径冲突必须报出来，不能默默取一个。
			//
			// structCount=1（M4）：冲突的包也被豁免了 ⇒ 计入结构性豁免计数。
			// 豁免不能静默——计数少报一次，就是让「豁免被默默吃掉」没有信号。
			// 逐条用例见 judge_shape_test.go（那儿带「坏实现会红」的对照断言）。
			name:        "同包同时在两张表 ⇒ 红在 conflict（口径冲突必须报出来）",
			exempt:      []string{"alpha", "beta"},
			structural:  []string{"alpha"},
			wantOK:      false,
			conflict:    1,
			structCount: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := options{min: 70, exempt: c.exempt, structuralExempt: c.structural, noTestExempt: noTestExemptAll}
			r := judge(parseLog(t), o)
			if r.ok() != c.wantOK {
				t.Fatalf("ok() = %v，期望 %v（报告：%+v）", r.ok(), c.wantOK, r)
			}
			if len(r.below) != c.below {
				t.Errorf("below = %v，期望 %d 条", r.below, c.below)
			}
			if len(r.staleExempt) != c.staleCov {
				t.Errorf("staleExempt = %v，期望 %d 条", r.staleExempt, c.staleCov)
			}
			if len(r.staleStructuralExempt) != c.staleStru {
				t.Errorf("staleStructuralExempt = %v，期望 %d 条", r.staleStructuralExempt, c.staleStru)
			}
			if len(r.conflictingExempt) != c.conflict {
				t.Errorf("conflictingExempt = %v，期望 %d 条", r.conflictingExempt, c.conflict)
			}
			if r.structuralExempted != c.structCount {
				t.Errorf("structuralExempted = %d，期望 %d 条", r.structuralExempted, c.structCount)
			}
			if len(r.inapplicableStructuralExempt) != c.inapplicable {
				t.Errorf("inapplicableStructuralExempt = %v，期望 %d 条",
					r.inapplicableStructuralExempt, c.inapplicable)
			}
		})
	}
}

// TestRunStructuralExemptFlagWired 锁死 flag → options 的接线。
//
// judge 层的用例**看不见 flag**：-structural-exempt 若被声明却没有传进 options，
// judge 的用例全绿，而真实门禁（走 run）仍然按老逻辑判红——这正是「判据被改了
// 但接线没跟上」的形状。故这条走 run，断言 flag 真的生效，且报告里**点名**了
// 结构性豁免的个数（豁免不能静默放行）。
func TestRunStructuralExemptFlagWired(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{
		"-min", "70",
		"-exempt", "beta",
		"-structural-exempt", "alpha",
		"-no-test-exempt", "cmd/tool,cmd/fixture,cmd/plain",
	}, strings.NewReader(syntheticLog), &out, &errOut)
	if code != 0 {
		t.Fatalf("alpha 走结构性豁免应放行，实际 code=%d，stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "1 个结构性豁免") {
		t.Fatalf("报告应点出结构性豁免计数，实际: %q", out.String())
	}
	if !strings.Contains(out.String(), "1 个覆盖率豁免") {
		t.Fatalf("报告应把两类豁免分开计数，实际: %q", out.String())
	}
}

// TestRunStructuralExemptNotMatchedFails 锁死「结构性豁免条目失效即红」在
// 进程层同样生效，且红文案**指名要改哪个变量**。
//
// 红得说不出原因，与常红同样会训练人忽略门禁（本仓在 cover-gate 的日志保留上
// 写过同一条）。
func TestRunStructuralExemptNotMatchedFails(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{
		"-min", "70",
		"-exempt", "beta",
		"-structural-exempt", "gone",
		"-no-test-exempt", "cmd/tool,cmd/fixture,cmd/plain",
	}, strings.NewReader(syntheticLog), &out, &errOut)
	if code == 0 {
		t.Fatalf("结构性豁免没命中任何包必须非零退出，stdout: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "PKG_COV_STRUCTURAL_EXEMPT") {
		t.Fatalf("应指名从哪个变量移除，实际: %q", errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("失败时 stdout 不该有输出，实际: %q", out.String())
	}
}
