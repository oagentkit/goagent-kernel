// judge.go —— 提交栅栏的**纯判据**：当前授权 vs 一次提交声明。
//
// 判据只有这一份：MemoryAuthority / Local / 宿主的共享权威实现都调用
// Judge，不得各写一套（第二份判据必然漂移——这与 taskboard.LeaseHeld
// 收敛「租约是否活持有」是同一条理由）。
//
// 判定次序是契约的一部分（见包文档）：
//
//	代次回退 → 代次超前 → 区域 → 令牌
//
// 先判代次再看区域：旧代 + 错区域的提交必须报「代次回退」（处置是按最新
// 代次对账），不能被报成「区域不是权威区域」（那会诱导调用方去查映射）。
package ownership

import "github.com/oekit/pkg/errors"

// Judge 判定一次提交声明是否与当前授权一致；nil = 放行。
//
// grant 必须是权威对 c.Domain 的**当前**授权（由调用方在同一原子操作内
// 取得）。函数本身无状态、无时钟，可在任何存储实现里复用。
func Judge(g Grant, c Commit) error {
	if g.Domain != c.Domain {
		return errors.Wrap(ErrUnknownDomain, "授权与提交声明的任务域不一致",
			"grant", string(g.Domain), "commit", string(c.Domain))
	}
	if c.Epoch < g.Epoch {
		return errors.Wrap(ErrStaleEpoch, "提交代次低于当前代次（旧代不得提交）",
			"domain", string(c.Domain), "commit", c.Epoch, "current", g.Epoch)
	}
	if c.Epoch > g.Epoch {
		return errors.Wrap(ErrEpochUnconfirmed, "提交代次高于当前代次（候选代次未经确认）",
			"domain", string(c.Domain), "commit", c.Epoch, "current", g.Epoch)
	}
	if c.Region != g.Region {
		return errors.Wrap(ErrNotOwner, "提交区域不是该任务域的权威区域",
			"domain", string(c.Domain), "commit", string(c.Region), "owner", string(g.Region))
	}
	// 空令牌的授权不得放行任何提交：这是「坏授权」而不是「无需令牌」。
	if g.Token == "" || c.Token != g.Token {
		return errors.Wrap(ErrTokenMismatch, "提交令牌不是当前授权的令牌（凭令牌认人）",
			"domain", string(c.Domain), "region", string(c.Region), "epoch", c.Epoch)
	}
	return nil
}
