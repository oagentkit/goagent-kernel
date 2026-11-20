// writers_test.go —— 写者数交叉校验（W1 存储侧 / W2 场景侧）的用例。
//
// 夹具（demoMatrix / demoSelection）见 check_test.go（同包共享）：
// demo-store 写者上限 1 且只认领单写者能力 cap-persist；
// demo-other 写者上限 unlimited 且认领多写者能力 cap-lease。
package main

import (
	"strings"
	"testing"
)

// TestCheckRejectsMissingMultiWriterAnnotation 能力没标 requires_multi_writer ⇒ 红。
//
// 缺省即红：漏登记 ⇒ 该能力对 W1/W2 从无下手 ⇒ 判据静默失效。
func TestCheckRejectsMissingMultiWriterAnnotation(t *testing.T) {
	m := strings.Replace(demoMatrix, "    requires_multi_writer: false\n", "", 1)
	root := writeRoot(t, m, demoSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有标注 requires_multi_writer")
}

// TestCheckRejectsMultiWriterStorageWithoutMultiWriterCapability W1 正向：
// 存储是多写者档位却没认领任何多写者能力 ⇒ 红。
//
// 这条是「两张表互相钉住」的牙齿：单改存储的 max_writers（把上限放宽）
// 就让「多写者场景不得选单写者档位」失效 ⇒ 必须当场红。
func TestCheckRejectsMultiWriterStorageWithoutMultiWriterCapability(t *testing.T) {
	s := strings.Replace(demoSelection, "    max_writers: 1\n", "    max_writers: 2\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有认领任何多写者能力")
}

// TestCheckRejectsSingleWriterStorageClaimingMultiWriterCapability W1 反向：
// 存储是单写者档位却认领多写者能力 ⇒ 红（声明面在吹牛）。
//
// 只判正方向会漏掉这一半：单写者档位认领多写者能力 = 声明面在吹牛。
func TestCheckRejectsSingleWriterStorageClaimingMultiWriterCapability(t *testing.T) {
	s := strings.Replace(demoSelection, "    max_writers: unlimited\n", "    max_writers: 1\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "却认领了多写者能力")
}

// TestCheckRejectsWeakenedScenarioWriters W2 正向：形态的写者数被改成 1，
// 但它下面的场景要求了多写者能力 ⇒ 红。
//
// 这条是「单改 scenario_writers 就让边界失效」的牙齿。
func TestCheckRejectsWeakenedScenarioWriters(t *testing.T) {
	s := strings.Replace(demoSelection, "  multi-node: 2\n", "  multi-node: 1\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "只有 1 个并发写者")
}

// TestCheckRejectsMultiWriterFormWithoutMultiWriterScenario W2 反向：
// 形态写者数 ≥2 却没有任何场景要求多写者能力 ⇒ 红。
func TestCheckRejectsMultiWriterFormWithoutMultiWriterScenario(t *testing.T) {
	m := strings.Replace(demoMatrix, "    required: [cap-lease]\n", "    required: [cap-persist]\n", 1)
	root := writeRoot(t, m, demoSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "都没有要求多写者能力")
}

// TestCheckRejectsNonPositiveScenarioWriters 形态的写者数 < 1 ⇒ 红。
//
// 写者数 0 / 负数没有语义 ⇒ 必须当场红，而不是被当成「不是多写者形态」
// 静默放过（那会让 W2 的两个分支都判不到它）。
func TestCheckRejectsNonPositiveScenarioWriters(t *testing.T) {
	s := strings.Replace(demoSelection, "  single-node: 1\n", "  single-node: 0\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "至少为 1")
}

// TestMultiWriterSetDerivedFromMatrix 多写者集合**从矩阵派生**：
// 把矩阵里的标注翻过来，判据跟着翻（代码里没写死任何能力 id）。
//
// 反向验证「没把多写者清单抄进代码」：抄进去的实现翻不动标注 ⇒ 这条红。
func TestMultiWriterSetDerivedFromMatrix(t *testing.T) {
	m := strings.Replace(demoMatrix, "    requires_multi_writer: false\n", "    requires_multi_writer: true\n", 1)
	root := writeRoot(t, m, demoSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	// demo-store 是单写者档位，却认领了（矩阵里被标成多写者的）cap-persist ⇒ 红。
	requireViolation(t, probs, "却认领了多写者能力")
}
