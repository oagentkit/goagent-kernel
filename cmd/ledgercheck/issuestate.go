// issuestate.go —— 台账 ↔ GitHub issue 的**状态双向对账**（issue #455）。
//
// # 这条判据补的是什么缺口
//
// 台账说某单「已合入」，而 GitHub 上它**还开着**——本轮（第二十六轮）就是这么
// 发现 #427 / #428 / #430 关漏的：台账 §32 登记「✅ 已合入」、关闭 PR 也 MERGED、
// 代码逐条核实落地，而三个 issue 全开着，没有任何机器会红。
//
// 与已登记的 #447（僵尸例外）是**同一类病**：声明面与事实面不一致，却没有判据会红。
// 差别只在漂移发生在**两个系统之间**（台账 ↔ GitHub），而不是同一份文件内部。
//
// # 判据面与数据源必须分离
//
// 判据是**纯函数**（台账 Entries + 一张 issue 开关状态表 ⇒ 违规清单），数据源是
// 可注入的适配层（生产走 `gh`，测试喂构造数据）。三条理由：
//
//  1. **判据要能离线测**：喂一张编出来的状态表就能钉住「漏关必须红」，不必联网；
//  2. **数据源要能换**：今天是 `gh` CLI，明天换 REST API 或快照文件，判据一个字不改；
//  3. **拿不到数据时必须给不出结论**（exit 2），不许静默当成「没有违反」——
//     后者正是本仓最贵的失效形态：门禁变绿。
//
// # 为什么不做「仓内快照」
//
// 快照要有人定期刷新，而没人刷新时它会**静默过期**：判据对着陈旧数据判、照常绿，
// 漏关的 issue 照常漏——把一个「没有判据」换成一个「判据会说谎」，没有变好。
// 本仓 CI 每次都有 `GITHUB_TOKEN`，实时查一次的代价是一次 API 调用，不值得引入
// 一份会过期的副本。
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"time"

	"github.com/oagentkit/runtime/procx"

	"github.com/oagentkit/goagent-kernel/internal/ledgerstatus"
)

// ghIssue 一条 issue 的开关状态（`gh issue list --json number,state` 的形状）。
type ghIssue struct {
	Number int    `json:"number"`
	State  string `json:"state"` // OPEN | CLOSED
}

// fetchIssueStates 数据源适配层：拉**全部** issue 的开关状态。
//
// 返回 `map[单号]是否仍开着`。拉不到（没有 gh / 没有网络 / 配额耗尽 / 仓库不可达）
// 一律返回 error——调用方据此 exit 2，而不是把「查不到」当成「没有违反」。
//
// 用 `gh` 而不是直连 REST API：gh 复用本机已有的认证、自动从 git remote 解析
// 仓库、且 CI 的 runner 自带它。代价是多一个外部二进制依赖，而它在本仓的两条
// 运行环境（开发者机 / GitHub Actions）里都必然存在。
func fetchIssueStates() (map[int]bool, error) {
	cmd := exec.Command("gh", "issue", "list",
		"--state", "all",
		"--limit", "1000",
		"--json", "number,state")
	// 子进程环境走唯一事实源 procx.ChildEnv，**不继承全量 environ**：本工具在
	// CI 里跑，父进程环境可能带凭据（GITHUB_TOKEN / 各种 key），而 gh 只需要
	// 白名单里的 PATH/HOME（找二进制与配置目录）加上两个 token 变量。
	//
	// 与 cmd/releasecheck 的 gitOut 同一条纪律：默认拒绝，按名显式放行。
	// 二进制查找不受影响——exec.Command 在**构造时**用父进程 PATH 做 LookPath。
	cmd.Env = procx.ChildEnv([]string{"GH_TOKEN", "GITHUB_TOKEN"})
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf(
				"拉取 GitHub issue 状态失败（gh 退出码 %d）：%s —— 门禁给不出结论时必须报错，"+
					"不许静默当成「没有违反」（检查 gh 是否安装并已登录、网络是否可达、GH_TOKEN 是否有效）: %w",
				len(ee.Stderr), string(ee.Stderr), err)
		}
		return nil, fmt.Errorf(
			"拉取 GitHub issue 状态失败（gh 不可用）：%w —— 门禁给不出结论时必须报错，"+
				"不许静默当成「没有违反」", err)
	}
	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("gh 输出不是合法 JSON：%w", err)
	}
	open := make(map[int]bool, len(issues))
	for _, is := range issues {
		open[is.Number] = is.State == "OPEN"
	}
	if len(open) == 0 {
		return nil, fmt.Errorf(
			"gh 返回 0 条 issue——空结果与「查不到」不可分，按给不出结论处理（不许静默当成没有违反）")
	}
	return open, nil
}

// checkIssueStates 判据 5（**纯函数**）：台账声明与 issue 实际开关状态双向对账。
//
// `open` 是 `单号 → 是否仍开着`；`open[单号]` 缺失表示 GitHub 上找不到它。
//
// 判两条方向，缺一条这条判据就是半个：
//
//   - **漏关**（本轮 #427/#428/#430 的形态）：台账说已合入，issue 还开着；
//   - **关了没登记**：台账说仍开着，issue 其实已关——接手的人会以为还没人做
//     （台账 §15 形态 (a) 的同一个坑，只是这次错在反方向）。
//
// 只判**登记面**的单号（`Entry.Registered`）：正文里被引用却没登记的单号由判据 3
// 管，不在这里重复报；台账**没说话**（StatusUnknown）的也不判——它不承担状态声明
// 的责任，判它等于把「没说」当成「说错了」。
func checkIssueStates(led *ledgerstatus.Ledger, open map[int]bool) []string {
	var probs []string
	for _, e := range led.Entries() {
		if !e.Registered {
			continue
		}
		actual, exists := open[e.Number]
		occ := registryOccurrence(e)
		loc := occ.File
		line := occ.Line
		if loc == "" {
			loc, line = e.Occurrences[0].File, e.Occurrences[0].Line
		}
		switch e.Status {
		case ledgerstatus.StatusClosed:
			switch {
			case !exists:
				probs = append(probs, fmt.Sprintf(
					"%s:%d: 单号 #%d 台账标「已合入」，GitHub 上却找不到它——声明指向一个不存在（或不可见）的单，无从核验",
					loc, line, e.Number))
			case actual:
				probs = append(probs, fmt.Sprintf(
					"%s:%d: 单号 #%d 台账标「已合入」，GitHub 上它**仍开着**——漏关（去关掉它；或它确实没做完，把台账状态改回「仍开着」）",
					loc, line, e.Number))
			}
		case ledgerstatus.StatusOpen:
			switch {
			case !exists:
				probs = append(probs, fmt.Sprintf(
					"%s:%d: 单号 #%d 台账记「仍开着」，GitHub 上却找不到它——声明指向一个不存在（或不可见）的单，无从核验",
					loc, line, e.Number))
			case !actual:
				probs = append(probs, fmt.Sprintf(
					"%s:%d: 单号 #%d 台账记「仍开着」，GitHub 上它**已关闭**——关了却没登记清偿（补「已合入 (#关闭PR)」；或它其实没做完，把 issue 重开）",
					loc, line, e.Number))
			}
		}
	}
	sort.Strings(probs)
	return probs
}

// issueSource 判据 5 的数据源结果：状态表 + 它是从哪来的。
type issueSource struct {
	// Open 单号 → 是否仍开着（与 fetchIssueStates 同形）。
	Open map[int]bool
	// Tag 进成功行的短标签：空 = 实时；退回快照时是「（⚠ 离线快照 <日期>，N 天前）」。
	Tag string
	// Warn 非空 ⇒ 调用方必须打印到 stderr：退回快照的完整原因。
	Warn string
}

// issueStatesWithFallback 判据 5 的**两级数据源**：实时 gh 为主，拉不到时退回仓内快照。
//
// # 为什么不是「拉不到就 exit 2」
//
// 门禁在**离线 / 没装 gh / 没有 token** 的机器上也该能跑——「跑不起来的门禁」与
// 「没有门禁」是同一件事，而开发者机离线跑 `make check` 是常态。快照就是给这种
// 情况兜底的：判定仍走同一条 `checkIssueStates`，换的只是喂给它的状态表。
//
// # 但兜底必须大声（否则就是「判据说谎」）
//
// 对着一份可能过期的数据判，最坏形态是**永远绿**——那把「没有判据」换成了
// 「判据会说谎」。所以退回时：`Tag` 进成功行、`Warn` 进 stderr，两处都点明
// 「数据可能不是当下事实」。**刷新是 `make issue-state` 的事**，判据本身不做
// 时效判定（时间判据会让门禁红在与被判事实无关的原因上）。
//
// 两级都拿不到才返回 error ⇒ 调用方 exit 2（给不出结论）。
func issueStatesWithFallback(fetcher func() (map[int]bool, error), snapPath string) (issueSource, error) {
	live, err := fetcher()
	if err == nil {
		return issueSource{Open: live}, nil
	}
	snap, date, age, serr := loadIssueSnapshot(snapPath)
	if serr != nil {
		return issueSource{}, fmt.Errorf(
			"实时拉取失败：%v；仓内快照也不可用（%s）：%w —— 两级都拿不到就给不出结论，"+
				"不许静默当成「没有违反」", err, snapPath, serr)
	}
	return issueSource{
		Open: snap,
		Tag:  fmt.Sprintf("（⚠ 离线快照 %s，%d 天前）", date, age),
		Warn: fmt.Sprintf(
			"⚠ ledgercheck: 实时拉取失败，已退回仓内快照（%s，%d 天前）——"+
				"判据现在对着快照在跑，数据可能不是当下事实；要刷新跑 make issue-state。原因：%v",
			date, age, err),
	}, nil
}

// loadIssueSnapshot 读仓内快照，返回「单号 → 是否仍开着」+ 日期 + 距今天数。
//
// 快照由 `make issue-state` 刷新（docs/issue-state.yaml，与台账同目录——两者是
// 同一件事的两面，分开放会让人只更新一个）。取值域由解析层封闭：未知取值报错，
// 不静默忽略——静默忽略会让那个单号从对账里消失，而消失的方向是门禁变绿。
func loadIssueSnapshot(path string) (map[int]bool, string, int, error) {
	snap, err := ledgerstatus.ParseIssueSnapshot(path)
	if err != nil {
		return nil, "", 0, err
	}
	open := make(map[int]bool, len(snap.Issues))
	for k, v := range snap.Issues {
		n, err := strconv.Atoi(k)
		if err != nil {
			return nil, "", 0, fmt.Errorf("快照 %s 里的键 %q 不是单号：%w", path, k, err)
		}
		open[n] = v == ledgerstatus.IssueOpen
	}
	if len(open) == 0 {
		return nil, "", 0, fmt.Errorf(
			"快照 %s 是空的——空结果与「查不到」不可分，按给不出结论处理", path)
	}
	return open, snap.Updated, snap.AgeDays(time.Now()), nil
}

// registryOccurrence 取该单号在**登记表里**的出现点（报错要指到那一行）。
func registryOccurrence(e ledgerstatus.Entry) ledgerstatus.Occurrence {
	for _, occ := range e.Occurrences {
		if occ.Kind == ledgerstatus.OccurrenceRegistry {
			return occ
		}
	}
	return ledgerstatus.Occurrence{}
}
