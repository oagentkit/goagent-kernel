// issuestate.go —— GitHub issue 开关状态**离线快照**的解析（issue #455 的数据源）。
//
// # 这一层只解析，不判定
//
// 判据 5（台账 ↔ GitHub issue 双向对账）的**判定**在 cmd/ledgercheck/issuestate.go
// （`checkIssueStates`，纯函数）——**判定只允许有一条路径**：两套判定各判一套必然
// 漂移，而漂移的方向恰好是「门禁判的和另一套判的不是一回事」。
//
// 本文件是那条判定的**数据源之一**：仓内快照 `docs/issue-state.yaml`（由
// `make issue-state` 刷新）。它与实时 `gh` 拉取是**两级关系**——实时为主，
// 拉不到时退回快照（见 cmd/ledgercheck 的 `issueStatesWithFallback`）。
//
// # 为什么快照这一级必须存在
//
// 门禁在离线 / 没装 gh / 没有 token 的机器上也要能跑——「跑不起来的门禁」与
// 「没有门禁」是同一件事。但快照会过期，所以退回时必须**大声回显**（进成功行 +
// 进 stderr），刷新是 `make issue-state` 的事。
//
// # 为什么解析要严格
//
// 取值域封闭、空文件报错、未知键报错：静默忽略一个未知取值会让那个单号从对账里
// 消失，而**消失的方向是门禁变绿**——本仓最贵的失效形态。
package ledgerstatus

import (
	"fmt"
	"os"
	"strconv"
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
// 解析失败一律响亮失败，**三种形态都报错**——静默降级的每一种都指向同一个结局
// （门禁给不出结论却显示成通过，是这条工具最贵的失效形态）：
//
//   - 未知取值（如 `closedx`）：会让那个单号从对账里消失；
//   - 非数字键（如 `abc`）：同上，且它多半是 YAML 写错了键名；
//   - 空快照：**空结果与「查不到」不可分**，拿它对账等于凭空判「全部一致」。
func ParseIssueSnapshot(path string) (*IssueSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap IssueSnapshot
	if err := yaml.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("解析 issue 状态快照失败（%s）: %w", path, err)
	}
	if len(snap.Issues) == 0 {
		return nil, fmt.Errorf(
			"issue 状态快照 %s 是空的（没有 issues 条目）——空结果与「查不到」不可分，"+
				"拿它对账等于凭空判「全部一致」；跑 make issue-state 重新生成", path)
	}
	for k, v := range snap.Issues {
		if _, err := strconv.Atoi(k); err != nil {
			return nil, fmt.Errorf(
				"issue 状态快照 %s 里的键 %q 不是单号——多半是 YAML 键名写错了"+
					"(静默跳过会让那一条从对账里消失，而消失的方向是门禁变绿)", path, k)
		}
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
