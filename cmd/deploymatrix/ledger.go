// ledger.go —— 台账视图（单号状态的唯一真相）。
//
// 存在的理由：部署清单此前把**任何** issue 证据都当成缺口，于是台账里早已
// 「✅ 已合入」的单（#262 / #273）仍被算成缺口 ⇒ 能力永远停在 partial ⇒
// 矩阵在骗宿主（说「你还缺这个」，其实早有了）。
//
// 口径只有一条：**issue 证据只有在对应单号「仍开着」时才算缺口**。
// 派生逻辑全部走 internal/ledgerstatus，本包不自己再读一遍台账——
// 两份解析必然漂移，而漂移的方向恰好是「清单判的和台账写的不是一套」。
package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yi-nology/goagent-kernel/internal/ledgerstatus"
)

// ledgerView 台账视图：把 issue 证据翻译成「还算不算缺口」。
type ledgerView struct {
	led *ledgerstatus.Ledger
}

// loadLedger 读台账。
//
// 读不到就是**判定面失效**，必须报错退出（退出码 2）：默默把每个 issue 都
// 当缺口会让矩阵永远停在 partial——那不是保守，是拿一个假象糊弄宿主。
func loadLedger(root string) (*ledgerView, error) {
	led, err := ledgerstatus.Parse(filepath.Join(root, ledgerstatus.DefaultPath))
	if err != nil {
		return nil, fmt.Errorf("读台账 %s 失败（单号状态不可派生，判定面失效）: %w",
			ledgerstatus.DefaultPath, err)
	}
	return &ledgerView{led: led}, nil
}

// issueGap 判定一条 issue 证据还算不算缺口。
//
// 返回 (是否缺口, 说明)。说明非空是要打给读者看的：派生不出状态时必须说清
// 凭什么这么判——「派生不出 ⇒ 静默当作没有」是最贵的失效形态。
//
// 派生不出时**按仍开着计**（缺口）：宁可说还缺，不许默默说齐了。
func (v *ledgerView) issueGap(ref string) (bool, string) {
	n, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	if err != nil {
		// 形态不对由 checkEvidence 报；这里按缺口计（保守方向）。
		return true, ""
	}
	switch v.led.Status(n) {
	case ledgerstatus.StatusClosed:
		pr, hasPR := v.led.ClosingPR(n)
		if hasPR {
			return false, fmt.Sprintf("issue %s 在台账里已合入（关闭 PR #%d）⇒ 不再算缺口", ref, pr)
		}
		return false, fmt.Sprintf("issue %s 在台账里已合入（关闭 PR 号不合法，由 ledgercheck 报）⇒ 不再算缺口", ref)
	case ledgerstatus.StatusOpen:
		return true, ""
	default:
		return true, fmt.Sprintf("issue %s 在台账里查不到状态（不可派生）⇒ 保守按仍开着计（仍算缺口）", ref)
	}
}
