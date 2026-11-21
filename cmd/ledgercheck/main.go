// Command ledgercheck 判演进台账自身的纪律（台账 = 单号状态的唯一真相）。
//
// 台账（docs/DISTRIBUTED-EVOLUTION-*.md）回答的是「这条单清了没」。它此前
// 只在散文里记这件事，机器读不出来——代价是台账 §15 形态 (a) 记着的实例：
// 一批单**早已由 8 个 PR 合入主干**，而接手的人按「单还挂着 open 标签」
// 判断「还没人做」，整条工作树作废。
//
// 现在台账是**声明面**：部署清单（cmd/deploymatrix）据此判定「某项能力还
// 缺不缺」，本命令据此判定「台账自己说的是不是真的」。
//
// # 四条判据（都能被机器判，失败形态是变红）
//
//  1. **同一单号各处状态必须一致**：一处写「已合入」、另一处写「仍开着」
//     ⇒ 自相矛盾，当天即红（报出是哪两处）。
//  2. **「已合入」必须带关闭 PR 号**（形如 #N）：否则「已清偿」无从核验。
//  3. **正文里被引用的单号必须在登记面登记**：漏登记 ⇒ 单号漂浮、无主。
//  4. **关闭 PR 号本身必须形如 #N**：写成别的形态（PR-201、#abc）一律红。
//
// 另有一条结构判据：表格列数与表头不齐要报出来（不静默吞掉）。
//
// # 判定面只有台账本身
//
// 解析走 internal/ledgerstatus（唯一真相），本命令不自己再读一遍 markdown——
// 两处各读一遍，结论就会分叉，而分叉的方向恰好是「门禁判的和台账写的
// 不是一套」。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/yi-nology/goagent-kernel/internal/ledgerstatus"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（台账读不到 / 参数错）。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ledgercheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录（从它向上找台账）")
	ledger := fs.String("ledger", ledgerstatus.DefaultPath, "台账路径（相对仓库根）")
	issueState := fs.String("issue-state", ledgerstatus.DefaultIssueStateFile,
		"issue 状态快照文件名（与台账同目录；由 make issue-state 刷新）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path, err := findLedger(*root, *ledger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ledgercheck: %v\n", err)
		return 2
	}
	led, err := ledgerstatus.Parse(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ledgercheck: %v\n", err)
		return 2
	}

	// issue 状态快照（issue #455）：判据面与数据源分离——本命令只读这份快照，
	// 绝不联网；刷新是 make issue-state 的事（那里才允许调 gh）。
	// 缺快照时**给不出结论**（退出码 2）而不是跳过：跳过等于这条判据静默不存在。
	snapPath := filepath.Join(filepath.Dir(path), *issueState)
	snap, err := ledgerstatus.ParseIssueSnapshot(snapPath)
	if err != nil {
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stderr,
				"ledgercheck: issue 状态快照 %s 不存在——它是本判据的输入，缺了就给不出结论：\n"+
					"  跑 make issue-state 生成（需要 gh 与网络；门禁本身不联网）\n", snapPath)
			return 2
		}
		_, _ = fmt.Fprintf(stderr, "ledgercheck: %v\n", err)
		return 2
	}

	probs := checkLedger(led)
	probs = append(probs, ledgerstatus.Reconcile(led, snap)...)
	if len(probs) == 0 {
		age := snap.AgeDays(time.Now())
		_, _ = fmt.Fprintf(stdout,
			"台账校验通过：%d 个单号 / %d 处出现点（issue 快照 %s，%d 天前）✓\n",
			len(led.Entries()), countOccurrences(led), snap.Updated, age)
		return 0
	}
	for _, p := range probs {
		_, _ = fmt.Fprintf(stdout, "✗ %s\n", p)
	}
	_, _ = fmt.Fprintf(stdout, "共 %d 处违反。判据见本命令的包头注释（含 issue #455 的双向对账）。\n", len(probs))
	return 1
}

// findLedger 自 start 向上找含台账的目录（与 cmd/deploymatrix 同口径：
// 门禁在仓库任意子目录里都要能跑）。
func findLedger(start, rel string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, rel)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("从 %s 向上找不到 %s——请在仓库内运行", start, rel)
		}
		dir = parent
	}
}

// countOccurrences 全部出现点数量（报告用）。
func countOccurrences(led *ledgerstatus.Ledger) int {
	n := 0
	for _, e := range led.Entries() {
		n += len(e.Occurrences)
	}
	return n
}
