// capabilities_test.go —— 能力侧与口径侧判据的用例。
//
// 夹具（demoMatrix / demoSelection 等）见 check_test.go（同包共享）。
package main

import (
	"strings"
	"testing"
)

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

// TestCheckRejectsBadEvidenceKind 证据类型不是 contract ⇒ 红。
func TestCheckRejectsBadEvidenceKind(t *testing.T) {
	s := strings.Replace(demoSelection, "          - kind: contract", "          - kind: doc", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "证据类型只允许 contract")
}

// TestCheckRejectsMissingSource 能力断言缺 source 标注 ⇒ 红。
func TestCheckRejectsMissingSource(t *testing.T) {
	s := strings.Replace(demoSelection, "        source: storage\n", "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "缺 source 标注")
}

// TestCheckRejectsUnknownSource source 取值不在词表内 ⇒ 红。
func TestCheckRejectsUnknownSource(t *testing.T) {
	s := strings.Replace(demoSelection, "source: storage", "source: magic", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "只允许")
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

// TestCheckRejectsDuplicateAsymmetryStorage 不对称的两侧必须互不相同 ⇒ 红。
//
// [X, X] 是「自己与自己比」⇒ 那不是不对称。
func TestCheckRejectsDuplicateAsymmetryStorage(t *testing.T) {
	s := strings.Replace(demoSelection, "storages: [demo-store, demo-other]", "storages: [demo-store, demo-store]", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "重复项")
}

// TestCheckRejectsEmptyPolicyNotes 选型口径为空 ⇒ 红。
func TestCheckRejectsEmptyPolicyNotes(t *testing.T) {
	s := strings.Replace(demoSelection, `  notes: |
    合成口径：单节点用 demo-store，多节点用 demo-other。
    审计路径必须走独立句柄。
`, "  notes: \"\"\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "policy.notes 为空")
}

// TestCheckRejectsMissingPolicyKeyword 口径里缺了要点要求的文本 ⇒ 红。
//
// 只判「非空」不够：把口径删到只剩一句空话照样绿 ⇒ 关键文本逐条核对。
func TestCheckRejectsMissingPolicyKeyword(t *testing.T) {
	s := strings.Replace(demoSelection, "    审计路径必须走独立句柄。\n", "    审计路径另说。\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有该要点要求的文本")
}

// TestCheckRejectsEmptyRequiredTexts 口径要点清单为空 ⇒ 红。
func TestCheckRejectsEmptyRequiredTexts(t *testing.T) {
	s := strings.Replace(demoSelection, `  required_texts:
    - id: demo-boundary
      keywords: ["独立句柄"]
`, "  required_texts: []\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "policy.required_texts 为空")
}

// TestPolicyTextIsCarriedToOutput 口径正文必须进输出（不然它只是没人读的散文）。
func TestPolicyTextIsCarriedToOutput(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	_, notes, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "审计路径必须走独立句柄") {
		t.Fatalf("口径正文必须进输出，实得:\n%s", joined)
	}
	if !strings.Contains(joined, "口径要点 demo-boundary") {
		t.Fatalf("口径要点必须进输出，实得:\n%s", joined)
	}
}

// TestAsymmetrySummariesAreCarriedToOutput 已知不对称的摘要必须进输出。
func TestAsymmetrySummariesAreCarriedToOutput(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	_, notes, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "两侧的读路由不同") {
		t.Fatalf("已知不对称的摘要必须进输出，实得:\n%v", notes)
	}
}
