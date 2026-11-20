// facade_test.go —— 门面「同款集合」判据的用例（台账 §28 建议 4）。
//
// 夹具是**合成数据**：两个假门面（pkgalpha / pkgbeta），各暴露同款的面
// 访问器。夹具里的包名与能力名都不指向任何真实领域——判据面是
// 「声明 ↔ 代码 ↔ 证据」的对应关系，那才是本门禁要钉的东西。
//
// ⚠ 每条判据都要有「**坏实现会红**」的用例：只测「好实现能过」，一个把
// 差集写反的错实现照样全过。
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// facadeMatrix 合成部署矩阵（两条能力、两个场景）。
const facadeMatrix = `version: 1
capabilities:
  - id: cap-persist
    title: 状态持久化
    requires_multi_writer: false
  - id: cap-shared
    title: 共享写入
    requires_multi_writer: true
scenarios:
  - id: scene-alpha
    title: 甲
    storage: alpha
    worker: single-node
    required: [cap-persist]
  - id: scene-beta
    title: 乙
    storage: beta
    worker: multi-node
    required: [cap-shared]
`

// facadeSelection 合成选型声明面：两个档位各一扇门面，面访问器同款。
//
// alpha 的 Events 面**没有**能力落点 ⇒ 登记在 known_unbacked_accessors；
// beta 的两个面都有落点 ⇒ 不登记。
const facadeSelection = `version: 1
policy:
  notes: |
    合成口径：两侧门面同款。
    审计路径必须走独立句柄。
  required_texts:
    - id: demo-boundary
      keywords: ["独立句柄"]
scenario_writers:
  single-node: 1
  multi-node: 2
storages:
  - id: alpha
    title: 合成档位甲
    kind: memory
    code_root: pkgalpha
    max_writers: 1
    facade:
      package: pkgalpha
      type: Store
      accessors: [Board, Events]
      other_methods: [Apply]
    capabilities:
      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgalpha/store.go#Store.Board
  - id: beta
    title: 合成档位乙
    kind: postgresql
    code_root: pkgbeta
    max_writers: unlimited
    facade:
      package: pkgbeta
      type: Store
      accessors: [Board, Events]
      other_methods: [Apply]
    capabilities:
      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgbeta/store.go#Store.Board
      - id: cap-shared
        source: storage
        evidence:
          - kind: contract
            ref: pkgbeta/store.go#Store.Events
known_unbacked_accessors:
  - storage: alpha
    accessor: Events
    reason: 合成夹具：能力表里没有以事件面为落点的能力。
`

// facadeAlphaSrc / facadeBetaSrc 合成代码：门面类型 + 两个面访问器 + 一个其它方法。
const facadeAlphaSrc = `package pkgalpha

// Store 合成门面类型。
type Store struct{}

// Board 合成面访问器。
func (s *Store) Board() {}

// Events 合成面访问器。
func (s *Store) Events() {}

// Apply 合成其它导出方法。
func (s *Store) Apply() error { return nil }
`

const facadeBetaSrc = `package pkgbeta

// Store 合成门面类型。
type Store struct{}

// Board 合成面访问器。
func (s *Store) Board() {}

// Events 合成面访问器。
func (s *Store) Events() {}

// Apply 合成其它导出方法。
func (s *Store) Apply() error { return nil }
`

// facadeRoot 造一个带两扇合成门面的仓根。
func facadeRoot(t *testing.T, selection string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs", "DEPLOY-MATRIX.yaml"), facadeMatrix)
	mustWrite(t, filepath.Join(root, "docs", "STORAGE-SELECTION.yaml"), selection)
	mustWrite(t, filepath.Join(root, "pkgalpha", "store.go"), facadeAlphaSrc)
	mustWrite(t, filepath.Join(root, "pkgbeta", "store.go"), facadeBetaSrc)
	return root
}

// facadeOnly 只跑门面侧判据（隔离其它判据的噪声）。
func facadeOnly(t *testing.T, root string) []string {
	t.Helper()
	sel, err := loadSelection(root)
	if err != nil {
		t.Fatalf("读声明面失败: %v", err)
	}
	probs, _ := checkFacades(root, sel)
	return probs
}

// facadeUnbackedBlock 合成登记面（alpha 的 Events 面无能力落点）。
const facadeUnbackedBlock = `known_unbacked_accessors:
  - storage: alpha
    accessor: Events
    reason: 合成夹具：能力表里没有以事件面为落点的能力。
`

// facadeAlphaFacadeBlock 合成门面声明段（alpha 的门面）。
const facadeAlphaFacadeBlock = `    facade:
      package: pkgalpha
      type: Store
      accessors: [Board, Events]
      other_methods: [Apply]
`

// facadeSharedStorage 合成「共用同一扇门」的另一个存储（清单故意不同）。
const facadeSharedStorage = `  - id: beta-shared
    title: 合成档位乙的部署形态
    kind: shared-authority
    code_root: pkgbeta
    max_writers: unlimited
    facade:
      package: pkgbeta
      type: Store
      accessors: [Board]
      other_methods: [Apply]
    capabilities:
      - id: cap-shared
        source: storage
        evidence:
          - kind: contract
            ref: pkgbeta/store.go#Store.Events
`

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
