package finalize

import (
	"context"

	"github.com/oekit/pkg/errors"
)

// Store 收尾存储面（宿主实现——gorm/model 不下沉）。
//
// 四个方法就是并发裁决算法的全部输入：条件迁移、报告覆写、回读现终态、
// 发事件——本包不碰任何存储客户端。
//
// # 接缝契约（宿主实现；内核在 Converge 里同步调用四个方法）
//
// @seam: no-reentry no-blocking panic-isolated
//
//   - no-reentry：宿主实现内**不得回查本包**（不得调 Converge）——那会递归
//     回本接口（Migrate → Converge → Migrate），无限递归。内核侧的兑现面：
//     Converge 在调用点**不持有任何互斥锁**，故回查不会自锁；剩下不可回查
//     的是本接口自身的递归。
//   - no-blocking：四个方法都被**同步等待**，且内核传下去的是
//     **Detached(ctx)**（脱离 ctx：切断取消传播）——它**不带期限**，所以
//     「不阻塞」在此处的准确含义是「有界」：宿主必须自备超时（需要时
//     context.WithTimeout(Detached(ctx), d)），不得无限等待、不得在持存储
//     内部锁时等别的回调。Publish 没有返回值却同样被同步等待，它的耗时
//     直接算在收尾时长里。
//   - panic-isolated：**宿主侧兑现**——本包**没有 recover 边界**：宿主实现
//     的 panic 会沿 Converge 外溢。Publish 尤其危险：它在**状态迁移已经
//     落地之后**才被调用，一旦 panic，整次收尾看起来失败，而状态其实已经
//     收敛——把已发生的事实改写成「没发生」。宿主实现须自行保证不 panic
//     （尤其 Publish）。
type Store interface {
	// Migrate 一次原子条件迁移：现状态 ∈ from 时迁移到 to；ok=false = 0 行。
	//
	// 多起点（from 多值）必须在**一次原子操作**里判（等价
	// `UPDATE … WHERE status IN (from…)`）：分多次逐点试会让两次尝试之间
	// 落地的他路写入漏判，且多出的往返都在脱离 ctx 预算内。原子性由宿主
	// 存储保证，本包只判结果。
	Migrate(ctx context.Context, from []string, to string) (ok bool, err error)
	// SetReport 覆写终报（折叠 / 追加策略留宿主；本包只传文本）。
	SetReport(ctx context.Context, text string) error
	// ReadStatus 回读库内现状态（零行补发「不谎报」的依据）。
	ReadStatus(ctx context.Context) (string, error)
	// Publish 发布收尾事件：Raced=true 表示条件迁移零行后的回读补发，
	// 此时 Status 必须取库内现状态——按意图报目标态就是谎报。
	Publish(ctx context.Context, n Notice)
}

// Notice 收尾事件（宿主映射到自己的事件总线 / 通知面）。
type Notice struct {
	// Status 事件携带的状态：命中 = 目标态；补发 = 库内现状态。
	Status string
	// Raced true = 条件迁移零行、他路已接手后的补发。
	Raced bool
	// Extra 宿主附加载荷（error、诊断字段等）。
	Extra map[string]any
}

// ZeroRow 零行（他路已接手）处置。
type ZeroRow int

const (
	// ZeroRowReadBack 零行时回读库内现状态并补发事件（Raced=true，不谎报）。
	// 零值即此——宁可多报一次真相，不可按意图谎报。
	ZeroRowReadBack ZeroRow = iota
	// ZeroRowSilent 零行时静默收手：不回读、不补发、**不写任何状态**。
	// 用于他路已收敛且事件已由他路发布的臂（终报在迁移前已按宿主策略
	// 写入，故此处的「不写」指不再动状态与事件）。
	ZeroRowSilent
)

// Spec 一次收敛的意图。
type Spec struct {
	// From 起点状态全集（如 running、waiting_approval——双起点缺一即搁浅，
	// 见包头纪律 ③）。
	From []string
	// To 目标态（contract 词表）。
	To string
	// Report 终报文本；空 = 不写（折叠 / 追加策略留宿主 SetReport）。
	Report string
	// ZeroRow 零行处置（缺省回读补发）。
	ZeroRow ZeroRow
}

// Outcome 收敛结果。
type Outcome struct {
	// Status 终局状态：命中 = 目标态；零行回读 = 库内现状态；静默 = 空。
	Status string
	// Hit 条件迁移命中（false = 他路已接手）。
	Hit bool
}

// Converge 收敛执行——包头三条并发纪律的执行面：
//
//   - ① 一切存储调用走 Detached 派生的脱离 ctx（编排 ctx 已取消也照写）；
//   - ② 零行 = 他路已接手，此后不再写任何状态；
//   - ③ 多起点一次原子迁移；零行缺省回读现状态补发事件（不谎报）。
//
// 终报写在迁移之前（取消 / 超时说明要留下，即使他路已先行收敛）；
// 终报写失败不阻断状态收敛——报告是展示面、状态是数据面，数据面优先。
func Converge(ctx context.Context, store Store, spec Spec) (Outcome, error) {
	if len(spec.From) == 0 {
		// 起点为空的条件迁移恒 0 行：与其让它静默走完零行分支，不如当场响亮失败。
		return Outcome{}, errors.New("收敛起点为空（Spec.From 至少要有一个起点状态）")
	}
	wctx := Detached(ctx)
	if spec.Report != "" {
		if err := store.SetReport(wctx, spec.Report); err != nil {
			log.CtxWarn(wctx, "收尾终报写入失败（状态收敛照做）", "err", err)
		}
	}
	hit, err := store.Migrate(wctx, spec.From, spec.To)
	if err != nil {
		return Outcome{}, errors.Wrap(err, "收尾条件迁移")
	}
	if hit {
		store.Publish(wctx, Notice{Status: spec.To})
		return Outcome{Status: spec.To, Hit: true}, nil
	}
	// 零行 = 他路已接手：不再写任何状态（纪律 ②）。
	if spec.ZeroRow == ZeroRowSilent {
		return Outcome{}, nil
	}
	got, err := store.ReadStatus(wctx)
	if err != nil {
		return Outcome{}, errors.Wrap(err, "收尾零行回读")
	}
	store.Publish(wctx, Notice{Status: got, Raced: true})
	return Outcome{Status: got}, nil
}
