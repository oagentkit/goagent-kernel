// matrix_selfcheck_test.go —— 故障矩阵自检与其反向验证（issue #274）。
// 从 matrix_test.go 拆出：结构阈值 400 行按判据的一侧切——矩阵声明与
// 门禁执行在一处，自检算法与「自检坏了必须红」的用例在另一处。
package faultmatrix

import (
	"fmt"
	"testing"
)

type cellKey struct {
	class faultClass
	mode  deploymentMode
}

// checkMatrix 矩阵自检的**唯一实现**（TestFaultDeploymentMatrix 与反向用例
// 共用同一份判定，避免门禁与自检各写一套）。
func checkMatrix(matrix []matrixCase, required []requiredCell) error {
	validMode := func(m deploymentMode) bool {
		return m == ModeEmbedded || m == ModeStandalone || m == ModeCluster
	}
	requiredClasses := map[faultClass]bool{}
	for _, r := range required {
		if !validMode(r.mode) {
			return fmt.Errorf("requiredMatrixCells 登记了未知模式 %q", r.mode)
		}
		if r.min <= 0 {
			return fmt.Errorf("requiredMatrixCells 的 %q/%s 下限必须为正（实得 %d）", r.class, r.mode, r.min)
		}
		requiredClasses[r.class] = true
	}

	names := map[string]bool{}
	cells := map[cellKey]int{}
	classes := map[faultClass]bool{}
	for _, c := range matrix {
		if c.name == "" {
			return fmt.Errorf("故障矩阵用例缺名字：%+v", c)
		}
		if names[c.name] {
			return fmt.Errorf("故障矩阵用例名重复：%s（名字是门禁报错的定位键）", c.name)
		}
		names[c.name] = true
		if len(c.modes) == 0 {
			return fmt.Errorf("故障矩阵用例 %s 未声明适用部署模式", c.name)
		}
		if c.run == nil && c.requiresEnv == "" {
			return fmt.Errorf("故障矩阵用例 %s 缺执行函数（非真环境格必须可执行）", c.name)
		}
		if c.requiresEnv != "" && c.localFact == nil {
			return fmt.Errorf("真环境格 %s 缺 localFact——Skip 之前必须判一条可判事实，否则跳过即假绿", c.name)
		}
		for _, m := range c.modes {
			if !validMode(m) {
				return fmt.Errorf("故障矩阵用例 %s 声明了未知部署模式 %q", c.name, m)
			}
			cells[cellKey{class: c.class, mode: m}]++
		}
		classes[c.class] = true
	}

	for class := range classes {
		if !requiredClasses[class] {
			return fmt.Errorf("故障矩阵出现未登记的故障类 %q——扩行必须先在 requiredMatrixCells 登记", class)
		}
	}
	for _, r := range required {
		if got := cells[cellKey{class: r.class, mode: r.mode}]; got < r.min {
			return fmt.Errorf("故障矩阵缺格：%q/%s 需要至少 %d 条用例，实得 %d——删格/漏格即门禁红",
				r.class, r.mode, r.min, got)
		}
	}
	return nil
}

// newFaultClasses issue #274 新增的 8 类（反向用例的删除集合）。
var newFaultClasses = map[faultClass]bool{
	ClassK8sAPIResponseLoss:   true,
	ClassCrossTenantCollision: true,
	ClassLongLeaseExpiry:      true,
	ClassDBFailover:           true,
	ClassEventReorder:         true,
	ClassQuotaDangling:        true,
	ClassControlPlaneRace:     true,
	// ClassNetworkPolicy 行已随 containerbackend 迁 goagent-runtime（v0.68.0），
	// 不在「删格必红」集合内——判据覆盖由该仓的 networkpolicy_matrix_test.go 兜。
}

// TestMatrixNewCasesNotDeletable 「删掉任一新用例 ⇒ 门禁红」的反向验证：
// 逐个删掉新增类的每一条用例，checkMatrix 都必须报错。
func TestMatrixNewCasesNotDeletable(t *testing.T) {
	removed := 0
	for i, c := range deploymentMatrix {
		if !newFaultClasses[c.class] {
			continue
		}
		reduced := make([]matrixCase, 0, len(deploymentMatrix)-1)
		reduced = append(reduced, deploymentMatrix[:i]...)
		reduced = append(reduced, deploymentMatrix[i+1:]...)
		if err := checkMatrix(reduced, requiredMatrixCells); err == nil {
			t.Fatalf("删除新用例 %q 未被矩阵自检拦截——「矩阵只准扩，不准删」没有执行体", c.name)
		}
		removed++
	}
	if removed == 0 {
		t.Fatal("矩阵里没有任何新增类用例——反向验证没有作用对象")
	}
}

// TestMatrixSelfCheckRejectsBadShape 自检逻辑本身的反向验证：形状坏了
// （缺 localFact / 真环境格缺 run 以外的必需面 / 重复名 / 未知模式 /
// 缺必需格）都必须红。
func TestMatrixSelfCheckRejectsBadShape(t *testing.T) {
	ok := func(*testing.T) {}
	required := []requiredCell{{class: ClassK8sAPIResponseLoss, mode: ModeCluster, min: 1}}
	good := []matrixCase{{
		class: ClassK8sAPIResponseLoss,
		name:  "good",
		modes: []deploymentMode{ModeCluster},
		run:   ok,
	}}
	if err := checkMatrix(good, required); err != nil {
		t.Fatalf("合法矩阵被误判: %v", err)
	}

	cases := []struct {
		name   string
		matrix []matrixCase
	}{
		{"删除必需格", nil},
		{"真环境格缺 localFact", []matrixCase{{
			class: ClassK8sAPIResponseLoss, name: "env-only",
			modes: []deploymentMode{ModeCluster}, requiresEnv: "某真集群",
		}}},
		{"非真环境格缺执行函数", []matrixCase{{
			class: ClassK8sAPIResponseLoss, name: "no-run",
			modes: []deploymentMode{ModeCluster},
		}}},
		{"重复用例名", []matrixCase{
			{class: ClassK8sAPIResponseLoss, name: "dup", modes: []deploymentMode{ModeCluster}, run: ok},
			{class: ClassK8sAPIResponseLoss, name: "dup", modes: []deploymentMode{ModeCluster}, run: ok},
		}},
		{"未知部署模式", []matrixCase{{
			class: ClassK8sAPIResponseLoss, name: "bad-mode",
			modes: []deploymentMode{"nomad"}, run: ok,
		}}},
		{"未登记的故障类", []matrixCase{{
			class: ClassEventReorder, name: "unregistered",
			modes: []deploymentMode{ModeCluster}, run: ok,
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkMatrix(tc.matrix, required); err == nil {
				t.Fatalf("坏矩阵未被自检拦截: %s", tc.name)
			}
		})
	}
}
