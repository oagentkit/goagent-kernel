// check_test.go —— 场景侧判据的用例与**合成夹具**。
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
    requires_multi_writer: false
    state: available
    evidence:
      - kind: contract
        ref: pkgdemo/store.go#Store
  - id: cap-lease
    title: 租约一致性
    requires_multi_writer: true
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
// demo-store 的能力是**存储自证型**（落点在 pkgdemo/ 下），写者上限 1；
// demo-other 的能力是**内核交付型**（落点也在 pkgdemo/ 下，但不在 pkgother/ 下
// ⇒ 标注与代码位置对得上），写者上限不限。
const demoSelection = `version: 1
policy:
  notes: |
    合成口径：单节点用 demo-store，多节点用 demo-other。
    审计路径必须走独立句柄。
  required_texts:
    - id: demo-boundary
      keywords: ["独立句柄"]
scenario_writers:
  single-node: 1
  multi-node: 2
storages:
  - id: demo-store
    title: 演示存储
    kind: memory
    code_root: pkgdemo
    max_writers: 1
    capabilities:
      - id: cap-persist
        source: storage
        evidence:
          - kind: contract
            ref: pkgdemo/store.go#Store
  - id: demo-other
    title: 演示另一种存储
    kind: postgresql
    code_root: pkgother
    max_writers: unlimited
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

// TestCheckRejectsEmptyDeclaration 声明面为空 ⇒ 红（空清单等于没有判据）。
func TestCheckRejectsEmptyDeclaration(t *testing.T) {
	root := writeRoot(t, demoMatrix, "version: 1\npolicy:\n  notes: 合成口径。\nstorages: []\n")
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有任何存储")
}

// TestScenarioNotesCoverEveryScenario 每个场景都要有一条说明。
//
// 可追溯性：读门禁输出的人要能逐场景看到「这个场景选了哪个 storage、
// 必需能力齐不齐」——只报违反不给场景全景，输出就少了这一层。
func TestScenarioNotesCoverEveryScenario(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	_, notes, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	joined := strings.Join(notes, "\n")
	for _, sc := range []struct{ id, storage string }{
		{"scene-one", "demo-store"},
		{"scene-two", "demo-other"},
	} {
		want := "场景 " + sc.id + " → storage " + sc.storage
		if !strings.Contains(joined, want) {
			t.Fatalf("说明里必须有 %q，实得:\n%s", want, joined)
		}
	}
}

// TestScenarioNoteNamesMissingCapabilities 必需能力不齐 ⇒ 说明行点名缺哪几项。
//
// 只说「不齐」等于让人自己去重推一遍 ⇒ 必须说清缺什么。
func TestScenarioNoteNamesMissingCapabilities(t *testing.T) {
	m := strings.Replace(demoMatrix, "required: [cap-persist]", "required: [cap-lease]", 1)
	root := writeRoot(t, m, demoSelection)
	probs, notes, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "不提供必需能力")
	joined := strings.Join(notes, "\n")
	want := "场景 scene-one → storage demo-store：必需能力不齐（缺 cap-lease）"
	if !strings.Contains(joined, want) {
		t.Fatalf("不齐的场景必须点名缺的能力（%q），实得:\n%s", want, joined)
	}
}

// TestCheckRejectsUnreferencedStorage 声明面里没有任何场景引用的 storage ⇒ 红。
//
// 陈旧声明当天即红（与仓内「陈旧包根当天即红」同款）：否则它会一直躺在
// 允许集里，直到某天有人以为它还有效。
func TestCheckRejectsUnreferencedStorage(t *testing.T) {
	s := strings.Replace(demoSelection, "known_asymmetries:", `  - id: demo-ghost
    title: 没有场景引用的档位
    kind: memory
    code_root: pkgother
    max_writers: unlimited
    capabilities:
      - id: cap-lease
        source: kernel
        evidence:
          - kind: contract
            ref: pkgdemo/lease.go#Lease
known_asymmetries:`, 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有任何场景引用")
}

// TestCheckRejectsMultiWriterOnSingleWriterStorage 多写者场景选了单写者档位 ⇒ 红。
//
// 这条是「硬边界不靠 capability 归属碰巧撑着」的牙齿：即使能力归属被人改对
// （给单写者档位补上多进程那几项），写者上限这条判据仍然红。
func TestCheckRejectsMultiWriterOnSingleWriterStorage(t *testing.T) {
	m := strings.Replace(demoMatrix, "storage: demo-other", "storage: demo-store", 1)
	root := writeRoot(t, m, demoSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "最多支持")
}

// TestCheckRejectsUnregisteredScenarioForm 场景形态没有登记写者数 ⇒ 红。
//
// 漏登记 = 判据无从下手 ⇒ 红（不判「没登记」就等于放过那条边界）。
func TestCheckRejectsUnregisteredScenarioForm(t *testing.T) {
	s := strings.Replace(demoSelection, "  multi-node: 2\n", "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有登记写者数")
}

// TestCheckRejectsMissingMaxWriters 存储没声明写者上限 ⇒ 红。
//
// 「没写」不等于「什么都行」——把它当成不限就是静默放行。
func TestCheckRejectsMissingMaxWriters(t *testing.T) {
	s := strings.Replace(demoSelection, "    max_writers: 1\n", "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "max_writers 未声明")
}

// TestCheckRejectsBadMaxWriters 写者上限取值非法 ⇒ 红。
func TestCheckRejectsBadMaxWriters(t *testing.T) {
	s := strings.Replace(demoSelection, "    max_writers: 1\n", "    max_writers: zero\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "max_writers 必须是数字或 unlimited")
}

// TestCheckRejectsZeroMaxWriters 写者上限为 0 ⇒ 红（至少 1）。
func TestCheckRejectsZeroMaxWriters(t *testing.T) {
	s := strings.Replace(demoSelection, "    max_writers: 1\n", "    max_writers: 0\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "max_writers 至少为 1")
}
