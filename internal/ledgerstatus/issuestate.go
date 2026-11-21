// issuestate.go —— GitHub issue 开关状态的**离线快照**与台账的双向对账（issue #455）。
//
// # 为什么判据不能直接调 GitHub
//
// 台账 §9 的登记表是**声明面**（「这条单清了没」），GitHub 是**事实面**。两者会漂：
// 2026-11-21 实录——台账把 #427 / #428 / #430 三条记成「✅ 已合入 (#436)」，代码也
// 逐条核实落地了，而 `gh issue list --state open` 里三条**全部还开着**（当轮手工关闭）。
//
// 但把 `gh` 调用焊进门禁是错的：CI 不一定有 GitHub token / 网络，门禁在离线机器上
// 必须照常能跑——**判据面与数据源分离**。故本文件只做两件事：
//
//  1. 解析一份**快照**（docs/issue-state.yaml，由 `make issue-state` 刷新）；
//  2. 拿快照与台账对账，两个方向都判。
//
// 判据因此是**纯函数**：输入两份文件的解析结果，输出违反清单。快照新不新是数据源
// 的事（报告里回显更新时间），判据只判「两份声明是否一致」。
package ledgerstatus

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// IssueState 是一个单号在 GitHub 上的开关状态（快照里的取值）。
//
// 取值域**封闭**：快照由 `make issue-state` 生成，手写出别的取值时解析即报错——
// 静默忽略一个未知取值会让那个单号从对账里消失，而消失的方向恰好是「判据变绿」。
type IssueState string

const (
	// IssueOpen 单号在 GitHub 上仍开着。
	IssueOpen IssueState = "open"
	// IssueClosed 单号在 GitHub 上已关闭。
	IssueClosed IssueState = "closed"
)

// IssueSnapshot 是「单号 → GitHub 开关状态」的离线快照。
//
// Updated 是刷新日期（UTC，YYYY-MM-DD）：它不参与判定，只在报告里回显——
// 快照过期时判据照样能跑，但读输出的人要看得见这份数据是哪天的。
type IssueSnapshot struct {
	// Updated 快照刷新日期（UTC，YYYY-MM-DD）。
	Updated string `yaml:"updated"`
	// Issues 单号（十进制字符串，不含 #）→ 开关状态。
	//
	// 键用 string 而不是 int：yaml 对整数键的解码路径与字符串键不同，
	// 而这里没有任何需要算术的场合——用 string 少一条分支。
	Issues map[string]IssueState `yaml:"issues"`
}

// DefaultIssueStateFile 是快照的文件名——它与台账**同目录**。
//
// 同目录是刻意的：两者是同一件事的两面（声明面 / 事实面），分开放会让人只更新
// 一个、另一个静静地过期，而那正是 #455 要治的失效形态。
const DefaultIssueStateFile = "issue-state.yaml"

// ParseIssueSnapshot 读取并解析一份 issue 状态快照。
//
// 解析失败一律响亮失败：未知取值 / 空文件都报错，不静默降级——门禁给不出结论却
// 显示成通过，是这条工具最贵的失败形态。
func ParseIssueSnapshot(path string) (*IssueSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap IssueSnapshot
	if err := yaml.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("解析 issue 状态快照失败（%s）: %w", path, err)
	}
	for k, v := range snap.Issues {
		if v != IssueOpen && v != IssueClosed {
			return nil, fmt.Errorf(
				"issue 状态快照 %s 里 #%s 的取值 %q 不合法：只能 %q 或 %q（取值域封闭，"+
					"未知取值会让那个单号从对账里消失，而消失的方向是门禁变绿）",
				path, k, v, IssueOpen, IssueClosed)
		}
	}
	return &snap, nil
}

// State 查一个单号在快照里的状态；第二个返回值表示快照里有没有它。
func (s *IssueSnapshot) State(n int) (IssueState, bool) {
	if s == nil || s.Issues == nil {
		return "", false
	}
	v, ok := s.Issues[fmt.Sprintf("%d", n)]
	return v, ok
}

// AgeDays 返回快照距今天数（按 Updated 算）；日期读不出来时返回 -1。
//
// 只用于回显，不参与判定——见文件头「判据只判两份声明是否一致」。
func (s *IssueSnapshot) AgeDays(now time.Time) int {
	if s == nil || s.Updated == "" {
		return -1
	}
	t, err := time.Parse("2006-01-02", s.Updated)
	if err != nil {
		return -1
	}
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// Reconcile 拿台账与 issue 状态快照做**双向**对账（issue #455 的判据本体）。
//
// 只判**登记面**里的单号：正文里顺带提到的号不是状态声明，拿它们去对 GitHub
// 会把「引用」误判成「声明」。
//
// 两个方向（失败形态都是变红，不是提示）：
//
//   - 台账说「已合入」、快照说 open ⇒ 台账说关了、事实面还开着；
//   - 台账说「仍开着」、快照说 closed ⇒ issue 已关但台账没登记关闭 PR 号。
//
// 另有一条：台账登记了、快照里却没有这个号 ⇒ 声明面指着一个 GitHub 上不存在的
// 对象（要么单号写错，要么快照没刷新）。
//
// 快照缺失的单号若台账也判不出状态（StatusUnknown），交给台账自身的矛盾判据去报，
// 这里不重复报——同一件事两个阻断计数只稀释信号。
func Reconcile(led *Ledger, snap *IssueSnapshot) []string {
	if led == nil || snap == nil {
		return nil
	}
	var probs []string
	for _, e := range led.Entries() {
		if !e.Registered {
			continue
		}
		ls := e.Status
		if ls == StatusUnknown {
			continue
		}
		gs, ok := snap.State(e.Number)
		if !ok {
			if ls == StatusClosed {
				probs = append(probs, fmt.Sprintf(
					"单号 #%d：台账登记为「已合入」，但 issue 状态快照里没有它——"+
						"GitHub 上找不到这个单号（跑 make issue-state 刷新快照），"+
						"或台账把单号写错了。声明面指着一个查无此号的对象，"+
						"「已合入」无从核验", e.Number))
			}
			continue
		}
		switch {
		case ls == StatusClosed && gs == IssueOpen:
			probs = append(probs, fmt.Sprintf(
				"单号 #%d：台账登记为「已合入 (%s)」，而 GitHub issue 状态快照是 %s——"+
					"台账说关了、事实面还开着（issue #455 同形失效）。"+
					"处置：把 issue 关掉，或把台账状态改回「仍开着」",
				e.Number, closingRefLabel(e), gs))
		case ls == StatusOpen && gs == IssueClosed:
			probs = append(probs, fmt.Sprintf(
				"单号 #%d：GitHub issue 状态快照是 %s，而台账登记为「仍开着」——"+
					"issue 已关但台账没登记关闭 PR 号（关早了，或台账没跟着翻面）。"+
					"处置：把状态列补成「✅ 已合入 (#N)」，N 是真正合入它的 PR",
				e.Number, gs))
		}
	}
	return probs
}

// closingRefLabel 给报错文案用的关闭 PR 号（没有时给一个占位，免得文案出现空括号）。
func closingRefLabel(e Entry) string {
	if e.ClosingPR == 0 {
		return "?"
	}
	return fmt.Sprintf("#%d", e.ClosingPR)
}
