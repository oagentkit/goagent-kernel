// stategate.go —— 交接单状态的**写路径闸**（issue #359）。
//
// 体检面（Check）能认出词表外状态并报 CodeUnknownState，写路径此前一个都
// 不消费这个事实：词表外状态被当成「非终态」，交接单照常被领走——内核在
// 自己读不懂的记录上铸造了领取令牌，还把状态改写成 accepted（原始状态
// 被抹掉，事后无从区分「这单本来就是词表外」与「正常领取」）。
//
// 故写路径统一消费 State.Valid()：读不懂 ⇒ 不写。闸只有这一份，三条写
// 路径（领取 / 回执 / 收回）共用——闸各写各的必然漏一条，而漏掉的那条
// 不会报错，只会让副作用照常施加。
package handoff

import (
	"github.com/oekit/pkg/errors"
)

// requireKnownState 读路径的状态闸（issue #359）：内核读不懂的状态不得写。
//
// 领取 / 回执 / 收回三条写路径共用这一道闸——闸各写各的 ⇒ 有的路径漏一道，
// 而漏掉的那条会在自己读不懂的记录上发放执行权或改写状态（失效形态是
// 绿的：领取成功、令牌可用、副作用照常施加）。
//
// what 只是给人看的动作名（领取 / 写回执 / 收回）；判定只有这一份。
func requireKnownState(rec Record, key Key, what string) error {
	if rec.Ticket.State.Valid() {
		return nil
	}
	return errors.Wrap(ErrUnknownState,
		"交接单状态不在词表内（内核读不懂，拒绝"+what+"）",
		"key", string(key), "state", string(rec.Ticket.State))
}
