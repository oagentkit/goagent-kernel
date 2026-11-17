// seam_test.go —— 接缝扫描器与接缝契约门禁的反向验证。
//
// # 这组用例钉住的是什么
//
// 三条判据各自会**静默失效**的形态：
//
//   - A 完备性：只判一个方向（只报未登记 / 只报陈旧登记）⇒ 另一半的偏离无声累积；
//   - B 成文率：标记解析写错（认不出 @seam: / 认错 token）⇒ 判据恒绿；
//   - C 自校验：段/id 的形状判错 ⇒ 拼错的 id 让那条接缝对门禁隐形。
//
// 夹具一律用**合成词**（Alpha / Beta / WithClock）——本仓的测试夹具不许带领域味。

package archcheck

import (
	"strings"
	"testing"
)

// disabledSeams 是一份「显式关闭接缝门禁」的声明面。
//
// 接缝段是 policy **必须显式声明**的东西：缺段由 policy 自校验报红
// （清单会变成死条文），而与本用例无关的门禁应当**显式关掉**，
// 而不是靠「没写就不判」蒙混过去。
func disabledSeams() *Seams {
	off := false
	return &Seams{Enabled: &off}
}

// seamFixtureSrc 是三类接缝各一的最小夹具。
//
// 三个接缝都写齐了契约标记——「缺标记」的那一档由删掉标记行的变体覆盖。
const seamFixtureSrc = `package p

// Alpha 是导出接口（宿主实现、内核调）。
//
// @seam: no-reentry no-blocking panic-isolated
type Alpha interface {
	Do() error
}

// Beta 是导出结构体，带一个函数型字段。
type Beta struct {
	// Hook 是宿主填的回调。
	//
	// @seam: no-reentry no-blocking panic-isolated
	Hook func() error
}

// WithClock 是导出构造函数，接一个函数型入参。
//
// @seam: no-reentry no-blocking panic-isolated
func WithClock(now func() int) *Beta { return nil }

// hidden 不导出：不是接缝声明点（导出才接得上宿主）。
func hidden(now func() int) {}

// unexportedField 不导出：宿主填不了，不是接缝声明点。
type Gamma struct {
	inner func() error
}
`

// seamFixtureRoot 造一个最小仓（go.mod + 一个包）。
func seamFixtureRoot(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{"p/a.go": src})
	return root
}

// seamFixtureIDs 是夹具里三条接缝的 id。
func seamFixtureIDs() []SeamRef {
	return []SeamRef{
		{ID: "example.test/h/p.Alpha"},
		{ID: "example.test/h/p.Beta.Hook"},
		{ID: "example.test/h/p.WithClock"},
	}
}

// TestScanSeamsFindsThreeKinds 扫描器必须认出三类接缝，且不认未导出的声明。
func TestScanSeamsFindsThreeKinds(t *testing.T) {
	root := seamFixtureRoot(t, seamFixtureSrc)
	seams, err := ScanSeams(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SeamKind{}
	for _, s := range seams {
		got[s.ID] = s.Kind
	}
	want := map[string]SeamKind{
		"example.test/h/p.Alpha":     SeamIface,
		"example.test/h/p.Beta.Hook": SeamFuncField,
		"example.test/h/p.WithClock": SeamFuncParam,
	}
	if len(got) != len(want) {
		t.Fatalf("应认出 %d 条接缝，实得 %d：%v", len(want), len(got), got)
	}
	for id, kind := range want {
		k, ok := got[id]
		if !ok {
			t.Fatalf("漏掉接缝 %s", id)
		}
		if k != kind {
			t.Fatalf("接缝 %s 的种类应是 %s，实得 %s", id, kind, k)
		}
	}
}

// TestScanSeamsSkipsTestFiles 测试文件不是接缝声明面。
func TestScanSeamsSkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"p/a.go":      seamFixtureSrc,
		"p/a_test.go": "package p\n\nfunc TestX() {}\n",
	})
	seams, err := ScanSeams(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range seams {
		if strings.Contains(s.File, "_test.go") {
			t.Fatalf("测试文件不在接缝扫描面内，实得 %s", s.ID)
		}
	}
}

// TestSeamContractViolationsCleanWhenComplete 标记齐 + 登记齐 ⇒ 零违反。
func TestSeamContractViolationsCleanWhenComplete(t *testing.T) {
	root := seamFixtureRoot(t, seamFixtureSrc)
	pol := &Policy{Seams: &Seams{Registry: seamFixtureIDs()}}
	vs, _, err := seamContractViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("样例齐备时不得报红，实得 %v", vs)
	}
}

// TestSeamContractViolationsFlagsMissingMarker 缺标记 ⇒ 红，且点名缺哪个 token。
func TestSeamContractViolationsFlagsMissingMarker(t *testing.T) {
	src := strings.Replace(seamFixtureSrc,
		"// @seam: no-reentry no-blocking panic-isolated\nfunc WithClock", "func WithClock", 1)
	root := seamFixtureRoot(t, src)
	pol := &Policy{Seams: &Seams{Registry: seamFixtureIDs()}}
	vs, _, err := seamContractViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("删掉标记行后必须恰好报 1 条，实得 %d：%v", len(vs), vs)
	}
	if vs[0].Kind != "seam_contract_missing" {
		t.Fatalf("红点必须指向「标记缺失」，实得 %s", vs[0].Kind)
	}
	for _, tok := range seamRequiredTokens {
		if !strings.Contains(vs[0].Text, tok) {
			t.Fatalf("报错必须点名缺失的 token %s，实得 %s", tok, vs[0].Text)
		}
	}
	if !strings.Contains(vs[0].Text, "example.test/h/p.WithClock") {
		t.Fatalf("报错必须点名是哪条接缝，实得 %s", vs[0].Text)
	}
}

// TestSeamContractViolationsFlagsPartialMarker 少一个 token ⇒ 红。
func TestSeamContractViolationsFlagsPartialMarker(t *testing.T) {
	src := strings.Replace(seamFixtureSrc,
		"// @seam: no-reentry no-blocking panic-isolated\nfunc WithClock",
		"// @seam: no-reentry\nfunc WithClock", 1)
	root := seamFixtureRoot(t, src)
	pol := &Policy{Seams: &Seams{Registry: seamFixtureIDs()}}
	vs, _, err := seamContractViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("少一个 token 必须报红，实得 %d：%v", len(vs), vs)
	}
	if !strings.Contains(vs[0].Text, "no-blocking") || !strings.Contains(vs[0].Text, "panic-isolated") {
		t.Fatalf("报错必须点名缺的两个 token，实得 %s", vs[0].Text)
	}
}

// TestSeamContractViolationsFlagsUnregistered 扫描到没登记 ⇒ 红（判据 A 正向）。
func TestSeamContractViolationsFlagsUnregistered(t *testing.T) {
	root := seamFixtureRoot(t, seamFixtureSrc)
	reg := seamFixtureIDs()
	pol := &Policy{Seams: &Seams{Registry: reg[:2]}}
	vs, _, err := seamContractViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Kind != "seam_unregistered" {
		t.Fatalf("未登记必须报红（seam_unregistered），实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "example.test/h/p.WithClock") {
		t.Fatalf("报错必须点名未登记的接缝，实得 %s", vs[0].Text)
	}
}

// TestSeamContractViolationsFlagsStaleRegistration 登记了扫描不到 ⇒ 红（判据 A 反向）。
//
// 这一条钉的是**双向**：只判未登记的话，接缝被改名/删除后登记不清理，
// 清单会变成死条文，而门禁照样绿。
func TestSeamContractViolationsFlagsStaleRegistration(t *testing.T) {
	root := seamFixtureRoot(t, seamFixtureSrc)
	reg := append(seamFixtureIDs(), SeamRef{ID: "example.test/h/p.Gone"})
	pol := &Policy{Seams: &Seams{Registry: reg}}
	vs, _, err := seamContractViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Kind != "seam_stale_registration" {
		t.Fatalf("陈旧登记必须报红（seam_stale_registration），实得 %v", vs)
	}
}

// TestSeamSummaryCountsByKindAndPackage 报告里的统计与待补清单必须可用。
func TestSeamSummaryCountsByKindAndPackage(t *testing.T) {
	src := strings.Replace(seamFixtureSrc,
		"// @seam: no-reentry no-blocking panic-isolated\nfunc WithClock", "func WithClock", 1)
	root := seamFixtureRoot(t, src)
	pol := &Policy{Seams: &Seams{Registry: seamFixtureIDs()}}
	_, summary, err := seamContractViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 3 {
		t.Fatalf("接缝总数应是 3，实得 %d", summary.Total)
	}
	if summary.ByKind[SeamIface] != 1 || summary.ByKind[SeamFuncField] != 1 || summary.ByKind[SeamFuncParam] != 1 {
		t.Fatalf("三类分布应是 1/1/1，实得 %v", summary.ByKind)
	}
	if len(summary.Packages) != 1 || summary.Packages[0].Package != "example.test/h/p" {
		t.Fatalf("待补清单必须按包分组，实得 %v", summary.Packages)
	}
	if len(summary.Packages[0].Pending) != 1 {
		t.Fatalf("待补清单必须只列缺标记的接缝，实得 %v", summary.Packages[0].Pending)
	}
}

// TestSeamSelfCheckRejectsBadRegistry 自校验必须抓住坏声明面。
func TestSeamSelfCheckRejectsBadRegistry(t *testing.T) {
	cases := map[string]*Policy{
		"缺段":   {},
		"空 id": {Seams: &Seams{Registry: []SeamRef{{ID: "  "}}}},
		"重复 id": {Seams: &Seams{Registry: []SeamRef{
			{ID: "example.test/h/p.Alpha"}, {ID: "example.test/h/p.Alpha"}}}},
		"形状非法": {Seams: &Seams{Registry: []SeamRef{{ID: "Alpha"}}}},
	}
	for name, pol := range cases {
		vs := seamSelfCheck("architecture-policy.yaml", pol)
		if len(vs) == 0 {
			t.Fatalf("%s：自校验必须报红", name)
		}
		for _, v := range vs {
			if v.Rule != selfCheckRuleID {
				t.Fatalf("%s：自校验违规必须挂在自校验规则下，实得 %s", name, v.Rule)
			}
		}
	}
}

// TestSeamSelfCheckAcceptsValidRegistry 合法声明面不得误报。
func TestSeamSelfCheckAcceptsValidRegistry(t *testing.T) {
	pol := &Policy{Seams: &Seams{Registry: []SeamRef{
		{ID: "example.test/h/p.Alpha"},
		{ID: "example.test/h/p.Beta.Hook"},
	}}}
	if vs := seamSelfCheck("architecture-policy.yaml", pol); len(vs) != 0 {
		t.Fatalf("合法声明面不得误报，实得 %v", vs)
	}
}

// TestSeamRuleIsRegisteredForReport 接缝契约规则必须登记进 res.Rules。
//
// 「新增结构规则必须同时登记进报告清单」：判了却不出现在清单里，
// 报告就看不出它判过什么——那条规则等于「判了但看不见」。
func TestSeamRuleIsRegisteredForReport(t *testing.T) {
	root := seamFixtureRoot(t, seamFixtureSrc)
	pol := &Policy{Seams: &Seams{Registry: seamFixtureIDs()}}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Rules {
		if r.ID == seamRuleID {
			return
		}
	}
	t.Fatalf("接缝契约规则必须登记进 res.Rules（否则判了但不回显），实得 %+v", res.Rules)
}

// TestRealPolicySeamRegistryMatchesScanner 真 policy 的接缝清单必须与扫描器一致。
//
// 这是判据 A 在仓库级的落点（issue #400 的验收标准）：
// 扫描器输出的接缝集合与门禁登记的接缝集合**逐条相等**（缺一即红）。
//
// 失败时这条用例会把「要补登记的」与「要删登记的」直接打出来——
// 改 policy 的 seams 段前先跑它，按输出誊一遍即可。
func TestRealPolicySeamRegistryMatchesScanner(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	seams, err := ScanSeams(root)
	if err != nil {
		t.Fatal(err)
	}
	scanned := map[string]bool{}
	for _, s := range seams {
		scanned[s.ID] = true
	}
	pol := loadRealPolicy(t)
	registered := map[string]bool{}
	for _, r := range pol.Seams.Registry {
		registered[r.ID] = true
	}
	var missing, stale []string
	for id := range scanned {
		if !registered[id] {
			missing = append(missing, id)
		}
	}
	for id := range registered {
		if !scanned[id] {
			stale = append(stale, id)
		}
	}
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("接缝清单与代码事实不等：\n需要补登记（%d）：%v\n需要删登记（%d）：%v",
			len(missing), missing, len(stale), stale)
	}
}
