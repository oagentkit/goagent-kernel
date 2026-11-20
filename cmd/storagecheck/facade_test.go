// facade_test.go —— 门面「同款集合」判据的用例（台账 §28 建议 4）。
//
// 合成夹具在 facade_fixtures_test.go：夹具与用例分文件，让两个文件各自留出
// 结构阈值的余量，而不是把用例文件顶到阈值线上。
//
// ⚠ 每条判据都要有「**坏实现会红**」的用例：只测「好实现能过」，一个把
// 差集写反的错实现照样全过。
package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestFacadeChecksPass 正常形态：门面侧零违反，且门禁整体绿。
func TestFacadeChecksPass(t *testing.T) {
	root := facadeRoot(t, facadeSelection)
	if probs := facadeOnly(t, root); len(probs) != 0 {
		t.Fatalf("正常形态不该报违反: %v", probs)
	}
	probs, notes, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("整体判定不该报违反: %v", probs)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "门面 pkgalpha#Store") {
		t.Fatalf("说明里必须有门面的形状，实得:\n%s", strings.Join(notes, "\n"))
	}
}

// TestFacadeRejectsUnregisteredMethod 代码里多一个没登记的导出方法 ⇒ 红（F1）。
func TestFacadeRejectsUnregisteredMethod(t *testing.T) {
	root := facadeRoot(t, facadeSelection)
	src := facadeAlphaSrc + "\n// Extra 未登记的导出方法。\nfunc (s *Store) Extra() {}\n"
	mustWrite(t, filepath.Join(root, "pkgalpha", "store.go"), src)
	requireViolation(t, facadeOnly(t, root), "未登记的导出方法")
}

// TestFacadeRejectsMissingTypeDeclaration 声明面的 facade.type 在门包里**不存在** ⇒ 红，
// 且报错要**同时**说清两个事实：类型根本不存在 **且** 派生集不完整（fail-closed）。
//
// 坏实现（把「找不到类型」当成「代码里没这个方法」⇒ 只报「登记与代码不符」）：门禁照样红，
// 但真病因是**类型名写错**（该改的是类型名，不是方法登记）⇒ 排查方向被带偏。
func TestFacadeRejectsMissingTypeDeclaration(t *testing.T) {
	s := strings.Replace(facadeSelection, "type: Store", "type: StoreGhost", 1)
	root := facadeRoot(t, s)
	probs := facadeOnly(t, root)
	requireViolation(t, probs, "找不到类型 StoreGhost 的声明")
	requireViolation(t, probs, "派生集不完整")
}

// TestFacadeRejectsAbsentMethod 登记了代码里没有的导出方法 ⇒ 红（F1 反向）。
func TestFacadeRejectsAbsentMethod(t *testing.T) {
	s := strings.Replace(facadeSelection, "other_methods: [Apply]", "other_methods: [Apply, Ghost]", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "但代码里没有这个导出方法")
}

// TestFacadeRejectsEmptyAccessors 面访问器为空 ⇒ 红（F2）。
func TestFacadeRejectsEmptyAccessors(t *testing.T) {
	s := strings.Replace(facadeSelection, "accessors: [Board, Events]", "accessors: []", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "accessors 为空")
}

// TestFacadeRejectsAccessorInBothLists 同一个名字同时在两个列表里 ⇒ 红（F2）。
func TestFacadeRejectsAccessorInBothLists(t *testing.T) {
	s := strings.Replace(facadeSelection, "other_methods: [Apply]", "other_methods: [Apply, Board]", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "同时出现在 accessors 与 other_methods")
}

// TestFacadeRejectsDuplicateAccessor 列表内部重复 ⇒ 红（F2）。
func TestFacadeRejectsDuplicateAccessor(t *testing.T) {
	s := strings.Replace(facadeSelection, "accessors: [Board, Events]", "accessors: [Board, Events, Events]", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "重复项")
}

// TestFacadeRejectsUnbalancedAccessorSets 两侧面访问器集合不等 ⇒ 红（F3）。
func TestFacadeRejectsUnbalancedAccessorSets(t *testing.T) {
	s := strings.Replace(facadeSelection, "accessors: [Board, Events]", "accessors: [Board]", 1)
	root := facadeRoot(t, s)
	probs := facadeOnly(t, root)
	requireViolation(t, probs, "面访问器集合不相等")
	if !strings.Contains(strings.Join(probs, "\n"), "Events") {
		t.Fatalf("报错要点名差哪几个，实得:\n%s", strings.Join(probs, "\n"))
	}
}

// TestFacadeRejectsUncoveredAccessor 面访问器既无落点又未登记 ⇒ 红（F4）。
func TestFacadeRejectsUncoveredAccessor(t *testing.T) {
	s := strings.Replace(facadeSelection, facadeUnbackedBlock, "", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "既没有能力落点，也没有登记进 known_unbacked_accessors")
}

// TestFacadeRejectsExpiredRegistration 登记项其实已被证据覆盖 ⇒ 红（F4 过期）。
func TestFacadeRejectsExpiredRegistration(t *testing.T) {
	s := strings.Replace(facadeSelection, "            ref: pkgalpha/store.go#Store.Board",
		"            ref: pkgalpha/store.go#Store.Board\n          - kind: contract\n            ref: pkgalpha/store.go#Store.Events", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "登记过期，当天删掉")
}

// TestFacadeRejectsUnknownRegistrationStorage 登记挂在未登记的存储上 ⇒ 红。
func TestFacadeRejectsUnknownRegistrationStorage(t *testing.T) {
	s := strings.Replace(facadeSelection, "  - storage: alpha", "  - storage: ghost", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "引用了未登记的存储")
}

// TestFacadeRejectsRegistrationWithoutReason 登记没写事实理由 ⇒ 红。
func TestFacadeRejectsRegistrationWithoutReason(t *testing.T) {
	s := strings.Replace(facadeSelection,
		"    reason: 合成夹具：能力表里没有以事件面为落点的能力。", "    reason: \"\"", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "缺 reason")
}

// TestFacadeRequiresDeclarationWhenMethodEvidence 有方法级落点却没写门面段 ⇒ 红（F5）。
func TestFacadeRequiresDeclarationWhenMethodEvidence(t *testing.T) {
	s := strings.Replace(facadeSelection, facadeAlphaFacadeBlock, "", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "必须登记 facade 段")
}

// TestFacadeRejectsDivergentSharedDeclaration 共用同一门面的两份声明不一致 ⇒ 红。
func TestFacadeRejectsDivergentSharedDeclaration(t *testing.T) {
	s := strings.Replace(facadeSelection, "  - id: beta", facadeSharedStorage+"  - id: beta", 1)
	root := facadeRoot(t, s)
	requireViolation(t, facadeOnly(t, root), "两份声明的面访问器/其它方法不一致")
}

// TestFacadeRejectsIncompleteDerivedSet 派生集不完整的三种形态 ⇒ 红（F1 的 fail-closed 侧）。
//
// ① 含嵌入字段；② **类型别名**（type Store = storeImpl）；③ **定义类型**（type Store storeImpl）。
// 坏实现（②③ 解析不出 struct 字段就当空集跳过 ⇒ 拿不完整的派生集去比）⇒ 全绿 ⇒ 必须失败。
func TestFacadeRejectsIncompleteDerivedSet(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"含嵌入字段", facadeAlphaShapeSrc, "嵌入字段"},
		{"类型别名", strings.Replace(facadeAlphaShapeSrc, "type Store struct{ Extra }", "type Store = storeImpl", 1), "类型别名"},
		{"定义类型", strings.Replace(facadeAlphaShapeSrc, "type Store struct{ Extra }", "type Store storeImpl", 1), "定义类型"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := facadeRoot(t, facadeSelection)
			mustWrite(t, filepath.Join(root, "pkgalpha", "store.go"), c.src)
			probs := facadeOnly(t, root)
			requireViolation(t, probs, c.want)
			requireViolation(t, probs, "门面类型 Store")
			requireViolation(t, probs, "存储 alpha")
		})
	}
}

// TestSameStringSetUsesSetSemantics sameStringSet 必须是**真集合**比较。
// 坏实现（逐元素包含判断）会让 [A,A] 与 [A,B] 返回「相等」⇒ 两份不一样的清单
// 被判成一样 ⇒ 共用门面的两份声明不一致时**判不出来**。
func TestSameStringSetUsesSetSemantics(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
		why  string
	}{
		{[]string{"A", "B"}, []string{"B", "A"}, true, "顺序不同、集合相同"},
		{[]string{"A", "A"}, []string{"A", "A"}, true, "同一份重复清单"},
		{[]string{"A", "A"}, []string{"A", "B"}, false, "重复项不能顶替另一个元素"},
		{[]string{"A"}, []string{"A", "B"}, false, "元素个数不同"},
		{[]string{"A", "B"}, []string{"A", "C"}, false, "元素不同"},
	}
	for _, c := range cases {
		if got := sameStringSet(c.a, c.b); got != c.want {
			t.Fatalf("%s：sameStringSet(%v, %v) = %v，期望 %v", c.why, c.a, c.b, got, c.want)
		}
	}
}

// TestRunDoesNotClaimFacadeChecksWithoutFacade 零门面 ⇒ ✓ 文案**不许**宣称判过门面那几条。
// 复现评审的 Major 2：删光 facade: 段 + 把方法级落点降级为类型级 ⇒ 门禁绿，但文案
// 若仍宣称「两侧门面的面访问器集合相等 + …」就是**谎报**（三项一项都没判）。
// ⚠ 本轮**不做**「零门面即红」——那会把门禁形状硬编码成「必须两扇门」（逼人编门，
// 且会打红既有合成夹具）⇒ 留作后续议题。
func TestRunDoesNotClaimFacadeChecksWithoutFacade(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection) // 合成夹具：零门面
	var stdout, stderr bytes.Buffer
	if rc := run([]string{root}, &stdout, &stderr); rc != 0 {
		t.Fatalf("零门面不该判红（本轮不做「零门面即红」），实得 %d；stderr=%s", rc, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "未判定") {
		t.Fatalf("零门面必须如实说「未判定」，实得:\n%s", out)
	}
	for _, claim := range []string{"两侧门面的面访问器集合相等", "门面的导出方法集合与代码派生集逐字相符"} {
		if strings.Contains(out, claim) {
			t.Fatalf("零门面时文案不得宣称判过 %q，实得:\n%s", claim, out)
		}
	}
	// F5 判的是「有方法级落点却没写 facade 段」⇒ 零门面下**照样判过**（口径不许含糊）。
	clause := out[strings.Index(out, "未判定"):]
	if i := strings.Index(clause, "；"); i >= 0 {
		clause = clause[:i]
	}
	if strings.Contains(clause, "F5") || !strings.Contains(out, "已判定") {
		t.Fatalf("F5 判过 ⇒ 不许与「未判定」并列，且必须如实说判过，实得:\n%s", out)
	}
}

// TestRunClaimsFacadeChecksWithFacades 有门面 ⇒ ✓ 文案**要**逐条列出判过什么。
// 反向用例：条件化不能变成「一律不说」——有门面时若不宣称，读的人就不知道绿盖住了什么。
func TestRunClaimsFacadeChecksWithFacades(t *testing.T) {
	root := facadeRoot(t, facadeSelection)
	var stdout, stderr bytes.Buffer
	if rc := run([]string{root}, &stdout, &stderr); rc != 0 {
		t.Fatalf("正常形态应退出 0，实得 %d；stderr=%s", rc, stderr.String())
	}
	out := stdout.String()
	for _, claim := range []string{"两侧门面的面访问器集合相等", "门面的导出方法集合与代码派生集逐字相符",
		"每个面访问器都有能力落点或已登记", "有方法级落点的存储必须登记门面段"} {
		if !strings.Contains(out, claim) {
			t.Fatalf("有门面时文案必须宣称判过 %q，实得:\n%s", claim, out)
		}
	}
}

// TestRunMarksFacadeParityUndecidedWithSingleFacade 恰好一扇门面 ⇒ F3 **未判定**，
// 其余判据（F1/F2/F4/F5）照常宣称判过。
//
// 为什么必须钉住这条：F3 的判定面是「**两侧**门面的面访问器集合相等」⇒ 只剩
// 一扇门时两侧比对退化成**自我比较（差集恒空 ⇒ 空真）**⇒ 此时宣称「判过」是
// 过度宣称 ⇒ 如实说「未判定」。这段条件化此前**零用例覆盖**（把整段撤掉，
// 测试套件仍全绿 ⇒ 那段代码不承重）⇒ 本条让它承重。
//
// ⚠ **两个方向都要钉**：
// ① F3 如实说「未判定」，且不许宣称「两侧门面的面访问器集合相等」；
// ② 其余判据**照常宣称判过**——不许把整段文案改成「一律未判定」
// （那是另一个方向的谎报：把判过的说成没判）。
// ⚠ 前提：单扇门是**合法形态** ⇒ 门禁本身必须绿（不许造出别的红来冒充
// 「未判定」这个结论）。
//
// 反向验证（坏实现会红）：把 main.go 里的 `if count < 2` 分支删掉（F3 一律
// 宣称已判）⇒ 本用例必须红；把分支还原 ⇒ 绿。原始输出见本轮修复报告。
func TestRunMarksFacadeParityUndecidedWithSingleFacade(t *testing.T) {
	root := facadeSingleRoot(t)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("单扇门面是合法形态，不该判红（不许造出别的红来冒充「未判定」），实得 %v", probs)
	}
	var stdout, stderr bytes.Buffer
	if rc := run([]string{root}, &stdout, &stderr); rc != 0 {
		t.Fatalf("单扇门面应退出 0，实得 %d；stderr=%s", rc, stderr.String())
	}
	out := stdout.String()
	// 方向一：F3 必须如实说「未判定」。
	if !strings.Contains(out, "F3 两侧门面对位 **未判定**") {
		t.Fatalf("单扇门面时 F3 必须如实说「未判定」，实得:\n%s", out)
	}
	if !strings.Contains(out, "门面判定面只有 1 扇") {
		t.Fatalf("未判定文案必须点出门面数（1 扇），实得:\n%s", out)
	}
	if strings.Contains(out, "两侧门面的面访问器集合相等") {
		t.Fatalf("单扇门面时不得宣称判过「两侧门面的面访问器集合相等」（自我比较 ⇒ 空真），实得:\n%s", out)
	}
	// 方向二：其余判据对那 1 扇门**照常判** ⇒ 文案必须逐条宣称判过。
	for _, claim := range []string{
		"门面清单形状合法（F2",
		"门面的导出方法集合与代码派生集逐字相符（F1）",
		"每个面访问器都有能力落点或已登记（F4）",
		"有方法级落点的存储必须登记门面段（F5）",
	} {
		if !strings.Contains(out, claim) {
			t.Fatalf("单扇门面时 %q 仍照常判过（不许与 F3 一起归进「未判定」），实得:\n%s", claim, out)
		}
	}
}
