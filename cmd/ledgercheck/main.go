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
// # 五条判据（都能被机器判，失败形态是变红）
//
//  1. **同一单号各处状态必须一致**：一处写「已合入」、另一处写「仍开着」
//     ⇒ 自相矛盾，当天即红（报出是哪两处）。
//  2. **「已合入」必须带关闭 PR 号**（形如 #N）：否则「已清偿」无从核验。
//  3. **正文里被引用的单号必须在登记面登记**：漏登记 ⇒ 单号漂浮、无主。
//  4. **关闭 PR 号本身必须形如 #N**：写成别的形态（PR-201、#abc）一律红。
//  5. **台账状态 ↔ GitHub issue 实际开关状态双向对账**（issue #455）：
//     台账说「已合入」而 issue 还开着（**漏关**），或台账说「仍开着」而 issue
//     已关（**关了没登记**）⇒ 当天即红。判据 1–4 判的是台账**内部**自洽，
//     这条判的是台账**说的和 GitHub 上的是不是一回事**——本轮 #427/#428/#430
//     就是这么关漏的（台账与 PR 都对，只有 issue 还开着，四条判据全绿）。
//
// 另有一条结构判据：表格列数与表头不齐要报出来（不静默吞掉）。
//
// # 判定面：台账 + 一次 GitHub 查询
//
// 解析走 internal/ledgerstatus（唯一真相），本命令不自己再读一遍 markdown——
// 两处各读一遍，结论就会分叉，而分叉的方向恰好是「门禁判的和台账写的
// 不是一套」。
//
// 判据 5 的数据源是**可注入的适配层**（生产走 gh，测试喂构造数据）：判据本身
// 是纯函数，能离线钉住「漏关必须红」；而**拉不到数据时给不出结论**（exit 2），
// 不许静默当成「没有违反」。详见 issuestate.go 的文件头。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yi-nology/goagent-kernel/internal/ledgerstatus"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// issueFetcher issue 状态数据源（**注入接缝**）：生产走 gh，测试喂构造数据——
// 判据 5 是纯函数、数据源可换，两者不互相绑架（详见 issuestate.go 文件头）。
//
// 用包级 var 而不是参数：run 的签名被既有用例占用，且这条接缝只在测试里换。
var issueFetcher = fetchIssueStates

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（台账读不到 / issue 状态拉不到 / 参数错）。
func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, stdout, stderr, issueFetcher)
}

// runWith 可注入数据源的实现（用例走它，生产走 run）。
func runWith(args []string, stdout, stderr io.Writer, fetcher func() (map[int]bool, error)) int {
	fs := flag.NewFlagSet("ledgercheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录（从它向上找台账）")
	ledger := fs.String("ledger", ledgerstatus.DefaultPath, "台账路径（相对仓库根）")
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

	probs := checkLedger(led)

	// 判据 5：台账状态 ↔ GitHub issue 实际状态（issue #455）。
	//
	// 数据源**两级**：实时 gh 为主（数据永远是当下事实），拉不到时退回仓内快照
	// （让门禁在离线 / 没装 gh 的机器上也能跑——「跑不起来的门禁」与「没有门禁」
	// 是同一件事）。判定只有一条路径 checkIssueStates：换的只是喂给它的状态表，
	// 两套判定各判一套必然漂移。
	//
	// 两级都拿不到 ⇒ 给不出结论（exit 2），既不并进 1（有违反）也不当 0（通过）：
	// 「查不到」与「没有违反」是两件事，混起来就是本仓最贵的失效形态。
	src, err := issueStatesWithFallback(fetcher,
		filepath.Join(filepath.Dir(path), ledgerstatus.DefaultIssueStateFile))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ledgercheck: %v\n", err)
		return 2
	}
	// 退回快照必须**大声**：判据现在对着一份可能过期的数据在跑。
	if src.Warn != "" {
		_, _ = fmt.Fprintf(stderr, "%s\n", src.Warn)
	}
	probs = append(probs, checkIssueStates(led, src.Open)...)

	if len(probs) == 0 {
		_, _ = fmt.Fprintf(stdout, "台账校验通过：%d 个单号 / %d 处出现点 / %d 条 issue 状态已对账%s✓\n",
			len(led.Entries()), countOccurrences(led), len(src.Open), src.Tag)
		return 0
	}
	for _, p := range probs {
		_, _ = fmt.Fprintf(stdout, "✗ %s\n", p)
	}
	_, _ = fmt.Fprintf(stdout, "共 %d 处违反。五条判据见本命令的包头注释。\n", len(probs))
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
