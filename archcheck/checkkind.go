// checkkind.go —— check_kind 的取值域与分派。
//
// # check_kind 是什么
//
// policy 的规则此前只有两种执行体：forbid_regex（判源码文本）与 forbid_import_regex
// （判 import 路径）。两者判的都是「某个形状在不在」，而有些纪律判的是**结构
// 关系**——「用例体内有没有 go 语句」与「测试侧有没有等待点」这两件事之间的关系。
// 逐行正则认不出这个关系（go 语句有多种形态，等待点还散在助手里），所以规则
// 显式声明 check_kind: goroutine_join，由本文件分派到 AST 判定体
// （testgoroutine.go）。
//
// # 取值域必须封闭
//
// 写错的类别名若被当成「不是已知类别」，这条登记就**静默降级**成「不判」：
// 规则在、severity 是 error、报告里照常计入条数，只是永不触发。失败形态是门禁
// **变绿**——比变红更贵，因为它看起来一切正常。故未知取值当天即红
// （policy 自校验，rulechecks.go 的规则级段）。
package archcheck

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
)

// checkKindGoroutineJoin 是 check_kind 的取值域：判定「用例内起的 goroutine
// 是否在测试侧被等待」。
const checkKindGoroutineJoin = "goroutine_join"

// knownCheckKind 判 check_kind 的取值是否已知。
//
// 取值域必须封闭的理由见文件头：写错的类别名若被当成「不是已知类别」，这条
// 登记就静默降级成「不判」——登记了一张表却没生效，且没有任何机器能发现。
func knownCheckKind(kind string) bool {
	switch kind {
	case checkKindGoroutineJoin:
		return true
	}
	return false
}

// knownCheckKindList 列出已知取值域（报错文案用）。
func knownCheckKindList() []string {
	return []string{checkKindGoroutineJoin}
}

// checkKindViolations 按 check_kind 分派到结构化执行体。
//
// 未知取值返回 error 而不是「零违规」：给不出结论却显示成通过，是本仓最贵的
// 失效形态（policy 自校验也报同一件事，那里能指出该改哪一行）。
func checkKindViolations(root string, r Rule) ([]Violation, error) {
	switch r.CheckKind {
	case checkKindGoroutineJoin:
		return testGoroutineViolations(root, r)
	default:
		return nil, fmt.Errorf("规则 %s 的 check_kind %q 未知（没有对应执行体）", r.ID, r.CheckKind)
	}
}

// gjSampleViolations 用**同一个判定核**判一条 policy 样例。
//
// 样例的 text 是一份**完整的 Go 源码**（package 行可省）：这条规则判的是结构
// 关系（用例体内的 go 语句 与 测试侧的等待点），样例必须能表达「用例 + 助手」
// 这样的多函数形态——单行文本承载不了。
//
// 走 gjJudgeFiles（与真判定同一份代码）⇒ 样例过了才说明真判定也过：两套实现
// 各自漂移，样例那套只看几个字符串、漂得更慢 ⇒ 绿灯更久，正是「从没见过它
// 触发的规则等于没有规则」最隐蔽的形态。
func gjSampleViolations(r Rule, text string) ([]Violation, error) {
	src := text
	if !strings.HasPrefix(strings.TrimSpace(src), "package ") {
		src = "package sample\n\n" + src
	}
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, sampleFileName(r), src, 0)
	if err != nil {
		return nil, fmt.Errorf("规则 %s 的样例不是可解析的 Go 源码: %w", r.ID, err)
	}
	return gjJudgeFiles([]gjFile{{rel: sampleFileName(r), fset: fset, file: af}}, r.ID), nil
}
