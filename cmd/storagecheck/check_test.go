// check_test.go —— 存储选型判据的用例（含三类反向验证）。
//
// 夹具是**合成数据**：两套假存储（各自一个能力）+ 两个场景 + 一条已知不对称。
// 夹具里的包名 / 能力名都不指向任何真实领域——判据面是「声明 ↔ 代码」的
// 对应关系，那才是本门禁要钉的东西。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// demoMatrix 合成部署矩阵：两个能力、两个场景。
const demoMatrix = `version: 1
capabilities:
  - id: cap-persist
    title: 状态持久化
    state: available
    evidence:
      - kind: contract
        ref: pkgdemo/store.go#Store
  - id: cap-lease
    title: 租约一致性
    state: available
    evidence:
      - kind: contract
        ref: pkgdemo/lease.go#Lease
scenarios:
  - id: scene-one
    title: 单节点
    storage: demo-store
    worker: single-node
    required: [cap-persist]
    readiness: available
  - id: scene-two
    title: 多节点
    storage: demo-other
    worker: multi-node
    required: [cap-lease]
    readiness: available
`

// demoSelection 合成选型声明面：两套存储各认领一个能力 + 一条已知不对称。
//
// demo-store 的能力是**存储自证型**（落点在 pkgdemo/ 下）；
// demo-other 的能力是**内核交付型**（落点也在 pkgdemo/ 下，但不在 pkgother/ 下
// ⇒ 标注与代码位置对得上）。
const demoSelection = `version: 1
policy:
  notes: |
    合成口径：够用即可。
storages:
  - id: demo-store
    title: 演示存储
    kind: file
    code_root: pkgdemo
    capabilities:
      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgdemo/store.go#Store
  - id: demo-other
    title: 演示另一种存储
    kind: file
    code_root: pkgother
    capabilities:
      - id: cap-lease
        source: kernel
        evidence:
          - kind: contract
            ref: pkgdemo/lease.go#Lease
known_asymmetries:
  - id: demo-gap
    storages: [demo-store, demo-other]
    summary: 两侧的读路由不同（合成示例）。
    evidence:
      - kind: contract
        ref: pkgdemo/store.go#Store.Apply
`

// demoStoreSrc 合成代码：门面类型 + 一个方法（供「类型.方法」形态落点用）。
const demoStoreSrc = `package pkgdemo

// Store 演示门面类型。
type Store struct{}

// Apply 演示方法。
func (s *Store) Apply() error { return nil }
`

// demoLeaseSrc 合成代码：另一个能力落点。
const demoLeaseSrc = `package pkgdemo

// Lease 演示能力面。
type Lease struct{}
`

// writeRoot 造一个最小的仓根（清单 + 声明面 + 被引用的合成代码）。
func writeRoot(t *testing.T, matrix string, selection string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs", "DEPLOY-MATRIX.yaml"), matrix)
	mustWrite(t, filepath.Join(root, "docs", "STORAGE-SELECTION.yaml"), selection)
	mustWrite(t, filepath.Join(root, "pkgdemo", "store.go"), demoStoreSrc)
	mustWrite(t, filepath.Join(root, "pkgdemo", "lease.go"), demoLeaseSrc)
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// requireViolation 断言判据报了违反（并把违反原文打出来，便于定位）。
func requireViolation(t *testing.T, probs []string, want string) {
	t.Helper()
	if len(probs) == 0 {
		t.Fatalf("应当报违反（含 %q），实得零违反", want)
	}
	joined := strings.Join(probs, "\n")
	if !strings.Contains(joined, want) {
		t.Fatalf("违反里没有 %q，实得:\n%s", want, joined)
	}
}

// TestCheckPasses 正常形态不报违反。
func TestCheckPasses(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	probs, notes, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("正常形态不该报违反: %v", probs)
	}
	if len(notes) == 0 {
		t.Fatal("正常形态必须给出派生说明（推导过程不给人看，人就得自己重推一遍）")
	}
}

// TestCheckRejectsStorageOutsideAllowedSet 场景声明了不在允许集内的 storage ⇒ 红。
func TestCheckRejectsStorageOutsideAllowedSet(t *testing.T) {
	m := strings.Replace(demoMatrix, "storage: demo-store", "storage: demo-ghost", 1)
	root := writeRoot(t, m, demoSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "不在选型声明面的允许集内")
}

// TestCheckRejectsMissingCapability storage 不提供场景必需的能力 ⇒ 红。
func TestCheckRejectsMissingCapability(t *testing.T) {
	m := strings.Replace(demoMatrix, "required: [cap-persist]", "required: [cap-lease]", 1)
	root := writeRoot(t, m, demoSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "不提供必需能力")
}

// TestCheckRejectsUnresolvableEvidence 能力断言指向不存在的符号 ⇒ 红。
func TestCheckRejectsUnresolvableEvidence(t *testing.T) {
	s := strings.Replace(demoSelection, "ref: pkgdemo/store.go#Store", "ref: pkgdemo/store.go#Nope", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "在代码里找不到")
}

// TestCheckRejectsMissingEvidenceFile 落点所在文件不存在 ⇒ 红。
func TestCheckRejectsMissingEvidenceFile(t *testing.T) {
	s := strings.Replace(demoSelection, "ref: pkgdemo/store.go#Store", "ref: pkgdemo/nope.go#Store", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "所在文件不存在")
}

// TestCheckRejectsCapabilityWithoutEvidence 能力断言没有落点 ⇒ 红。
//
// 「派生不出」若被当成「没有手抄」，门禁就是静默空转：一条都判不了却照常报绿。
func TestCheckRejectsCapabilityWithoutEvidence(t *testing.T) {
	s := strings.Replace(demoSelection, `        evidence:
          - kind: contract
            ref: pkgdemo/store.go#Store
`, "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有落点")
}

// TestCheckRejectsUncoveredMatrixCapability 矩阵里的能力没有任何存储认领 ⇒ 红。
//
// 没人认领的能力 = 该能力不被任何场景判定 ⇒ 矩阵与选型面之间开了一个洞。
func TestCheckRejectsUncoveredMatrixCapability(t *testing.T) {
	s := strings.Replace(demoSelection, `      - id: cap-lease
        source: kernel
        evidence:
          - kind: contract
            ref: pkgdemo/lease.go#Lease
`, "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有任何存储认领")
}

// TestCheckRejectsUnknownCapabilityID 声明了矩阵能力表里没有的能力 ⇒ 红。
func TestCheckRejectsUnknownCapabilityID(t *testing.T) {
	s := strings.Replace(demoSelection, "      - id: cap-persist", "      - id: cap-ghost", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "不在部署矩阵的能力表里")
}

// TestCheckRejectsMislabelledSource 内核交付型却把落点指在本存储包内 ⇒ 红。
//
// 标注与代码位置对不上 = 声明面在撒谎（「PG 提供了 job-idempotency」）。
func TestCheckRejectsMislabelledSource(t *testing.T) {
	s := strings.Replace(demoSelection, `      - id: cap-lease
        source: kernel
`, `      - id: cap-lease
        source: storage
`, 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "标注为存储自证型")
}

// TestCheckRejectsKernelLabelOnOwnPackage 存储自证型却把落点指在包外 ⇒ 红。
func TestCheckRejectsKernelLabelOnOwnPackage(t *testing.T) {
	s := strings.Replace(demoSelection, `      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgdemo/store.go#Store
`, `      - id: cap-persist
        source: kernel
        evidence:
          - kind: contract
            ref: pkgdemo/store.go#Store
`, 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "标注为内核交付型")
}

// TestCheckRejectsMalformedEvidenceRef 落点不是 path#Symbol 形态 ⇒ 红。
func TestCheckRejectsMalformedEvidenceRef(t *testing.T) {
	s := strings.Replace(demoSelection, "ref: pkgdemo/store.go#Store", "ref: pkgdemo/store.go", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "path#Symbol")
}

// TestCheckRejectsBadAsymmetry 不对称登记必须落在两个真实存储上 ⇒ 红。
func TestCheckRejectsBadAsymmetry(t *testing.T) {
	s := strings.Replace(demoSelection, "storages: [demo-store, demo-other]", "storages: [demo-store]", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "至少两个存储")
}

// TestCheckRejectsUnknownAsymmetryStorage 不对称登记引用了不存在的存储 ⇒ 红。
func TestCheckRejectsUnknownAsymmetryStorage(t *testing.T) {
	s := strings.Replace(demoSelection, "storages: [demo-store, demo-other]", "storages: [demo-store, demo-ghost]", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "未登记的存储")
}

// TestCheckRejectsEmptyDeclaration 声明面为空 ⇒ 红（空清单等于没有判据）。
func TestCheckRejectsEmptyDeclaration(t *testing.T) {
	root := writeRoot(t, demoMatrix, "version: 1\npolicy:\n  notes: 合成口径。\nstorages: []\n")
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有任何存储")
}

// TestCheckRejectsEmptyPolicyNotes 选型口径为空 ⇒ 红。
func TestCheckRejectsEmptyPolicyNotes(t *testing.T) {
	s := strings.Replace(demoSelection, `policy:
  notes: |
    合成口径：够用即可。
`, "policy:\n  notes: \"\"\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "policy.notes 为空")
}

// TestDeclaredSymbolsFindsMethodForm 符号解析认「类型.方法」形态（go/ast 判定）。
func TestDeclaredSymbolsFindsMethodForm(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	syms, err := declaredSymbols(filepath.Join(root, "pkgdemo", "store.go"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !syms["Store"] {
		t.Fatal("应找到类型 Store")
	}
	if !syms["Store.Apply"] {
		t.Fatal("应找到方法 Store.Apply")
	}
	if syms["Store.Nope"] {
		t.Fatal("不该找到 Store.Nope")
	}
	if syms["Nope"] {
		t.Fatal("不该找到 Nope")
	}
}
