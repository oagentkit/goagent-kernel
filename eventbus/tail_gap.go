// tail_gap.go —— 游标空洞哨兵（issue #183）；从 tail.go 拆出以守住文件行数阈值。
package eventbus

import "context"

// reportGap 空洞哨兵：缺省走包内 warn，装配了 WithTailOnGap 则交给宿主
// （接告警/指标），两者不叠发——注入即接管留痕通道。
func (t *Tailer) reportGap(ctx context.Context, from, to uint64) {
	if t.onGap != nil {
		t.onGap(from, to)
		return
	}
	log.CtxWarn(ctx, "event-tail 游标空洞（TailStore 违反无空洞推进契约，被跳 ID 可能永久丢失）",
		"gap_from", from, "gap_to", to)
}
