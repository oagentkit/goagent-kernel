// Command deploymatrix 校验部署配方能力清单（docs/DEPLOY-MATRIX.yaml）。
//
// 存在意义（issue #277）：部署形态的推荐组合此前散落在 USAGE、examples 包
// 文档与外部评审的场景表里，没有一份可查、可比的清单回答「这套组合缺哪些
// 能力才能上生产」。本命令把清单变成**可执行声明**：
//
//  1. 每条能力的证据必须可解析：
//     - contract: path#Symbol —— 文件存在且该符号真的声明在文件里（go/ast）；
//     - gate: target —— Makefile 里有这个目标；
//     - issue: #N —— 该单号出现在仓内 docs/*.md 或 CHANGELOG（本地可查）；
//     - doc: path —— 文件存在；
//  2. 能力的 state（available / partial / missing）必须与证据形态一致：
//     - available = 至少一条 contract/gate 且无 issue 缺口；
//     - partial   = 既有 contract/gate 又有 issue 缺口；
//     - missing   = 只有 issue（还没有可查的契约/门禁落点）；
//     其中「issue 缺口」= **该单号在台账里仍开着**：台账（docs/DISTRIBUTED-
//     EVOLUTION-*.md）是单号状态的唯一真相，已合入的单不再是缺口——
//     把它算成缺口，能力就永远停在 partial，矩阵等于对宿主说「你还缺这个」
//     而它其实早有了（台账 §15 形态 (a) 的同一个坑的另一个面）；
//  3. 场景的 readiness 必须等于必需能力的最坏状态；
//  4. docs/USAGE.md 的 deploy-matrix 机器口径块必须与清单逐行一致
//     （场景 id / 推荐存储 / Worker 模式 / 当前状态）——不一致即红；
//  5. 未登记 / 重复 / 引用失效 / 状态谎报一律红。
//
// 失败形态是**变红**：清单缺失、USAGE 块缺失、证据引用失效都报错退出，
// 不静默跳过。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	root, err := findRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploymatrix: %v\n", err)
		return 2
	}
	code, err := run(root, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploymatrix: %v\n", err)
		return 2
	}
	return code
}

// findRoot 自 cwd 向上找含清单文件的目录（与 cmd/apilifecheck 同口径：
// 门禁在仓库任意子目录里都要能跑）。
func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, matrixPath)); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("从 %s 向上找不到 %s——请在仓库内运行", start, matrixPath)
		}
		dir = parent
	}
}

// run 完整判定：返回退出码。err 非空 = 给不出结论（退出码 2）。
func run(root string, out io.Writer) (int, error) {
	m, err := loadMatrix(root)
	if err != nil {
		return 0, err
	}
	probs, notes, err := checkMatrix(root, m)
	if err != nil {
		return 0, err
	}
	// 说明先打：它们解释「某个状态是怎么推出来的」，读者不看就无从判断
	// 这个结论对不对（派生状态的理由不给人看，就等于让人自己重推一遍）。
	for _, n := range notes {
		_, _ = fmt.Fprintf(out, "⚠ %s\n", n)
	}
	if len(probs) == 0 {
		_, _ = fmt.Fprintf(out, "部署矩阵校验通过：%d 个场景 / %d 项能力（%s）✓\n",
			len(m.Scenarios), len(m.Capabilities), stateSummary(m.Scenarios))
		return 0, nil
	}
	for _, p := range probs {
		_, _ = fmt.Fprintf(out, "✗ %s\n", p)
	}
	_, _ = fmt.Fprintf(out, "共 %d 处违反。判定口径见 %s 头注与 docs/USAGE.md 的 deploy-matrix 块。\n",
		len(probs), matrixPath)
	return 1, nil
}

// stateSummary 场景就绪态的计数摘要（报告用）。
func stateSummary(scenarios []Scenario) string {
	counts := map[string]int{}
	for _, s := range scenarios {
		counts[s.Readiness]++
	}
	return fmt.Sprintf("available %d / partial %d / missing %d",
		counts[StateAvailable], counts[StatePartial], counts[StateMissing])
}
