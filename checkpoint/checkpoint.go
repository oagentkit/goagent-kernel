// Package checkpoint 编排续跑契约：编排进度快照的**存取边界**与**恢复判定机制**。
//
// 生命周期: Stable（v0.33.0 定级）——只允许 additive 变更（新增导出符号 /
// 新增方法且给零值默认实现 / 新增错误值）；改签名、改语义、改默认值、
// 删导出符号都算 breaking，判定规则与变更流程见 docs/api-compat.md，
// 逐包理由见 api-lifecycle.yaml。
//
// # 为什么需要这个包
//
// 「编排是代码，LLM 只做单环节」的必然推论是「崩溃后能接着跑」。此前本仓把编排
// 状态全押在宿主 DB 上（eventbus 的模块说明写明「持久化在发布点由宿主负责」），
// 内核自己回答不了两个问题：
//
//  1. 这一步执行到哪了？
//  2. 进程重启后该从哪继续？
//
// 这不是宿主能替内核决定的——它决定的是编排语义，不是存储布局。
//
// # 关键取舍：续跑而非重放
//
// 恢复有两种形态，处置完全不同：
//
//   - **重放**：从第一步重跑。编排是代码所以结果是确定的，但**已产生的副作用会再来一次**。
//   - **续跑**：只跑未完成的部分。副作用不重复，但代价是依赖快照足够可信。
//
// 本包选续跑，并把该选择做成**可机械判定的纯函数**（BuildPlan），而不是留给宿主
// 在崩溃那一刻临场决定——临场决定必然选最坏的那个。
//
// # 边界
//
// 与 contract 同口径：**编排语义字段定死，领域字段走开放键**。
// 内核解释 Done/Seq/Status，不解释 Fields 里的任何键。
package checkpoint

import (
	"context"
	"sync"

	"github.com/oekit/pkg/errors"
)

// 哨兵错误。宿主实现 Load 时无记录**必须**返回 ErrNotFound，
// 不得返回 (零值, nil)——那会被上游误判成「这个 run 从没跑过」从而全量重跑，
// 重复副作用正是续跑要避免的那件事。
var (
	// ErrNotFound 该 runID 无进度快照（首次执行的正常情形）。
	ErrNotFound = errors.New("编排进度快照不存在")

	// ErrStaleSeq 快照序号未严格递增，被单调闸拒绝。
	ErrStaleSeq = errors.New("编排进度快照序号回退")
)

// Snapshot 一次编排进度快照。
//
// 粒度约定：**每完成一个步骤落一次**。粒度更细则恢复点更精确但写放大，
// 更粗则在崩溃时重做更多——宿主按自己副作用的代价选，本包不定死。
type Snapshot struct {
	// RunID 编排运行标识（宿主分配，须稳定且可跨进程重建）。
	RunID string

	// Seq 单调序号。同一 RunID 下**严格递增**（见 SeqGate）。
	// 取 0 表示「尚无快照」。
	Seq uint64

	// Status 编排语义状态，取值见 contract 包的状态常量集。
	// 内核据此判「这次运行是否已终结」，不解释具体语义。
	Status string

	// Done 已完成步骤的 ID，**无序要求**（按完成顺序追加即可）。
	// 内核按**依赖**判定可信边界，不依赖这里的顺序。
	Done []string

	// Fields 宿主领域状态走开放键（目标、策略档、租户……）。
	// 内核不解释键的含义、不校验、不假设它存在。
	Fields map[string]string
}

// Clone 深拷贝快照。宿主实现 Store 时**必须**拷贝而非持引用：
// 快照若被调用方在落库后继续改动，恢复判定的依据就不再可信。
func (s Snapshot) Clone() Snapshot {
	out := s
	if s.Done != nil {
		out.Done = append([]string(nil), s.Done...)
	}
	if s.Fields != nil {
		out.Fields = make(map[string]string, len(s.Fields))
		for k, v := range s.Fields {
			out.Fields[k] = v
		}
	}
	return out
}

// Store 进度快照的存取边界（唯一接缝）。
//
// 实现须保证：
//   - Save 对同一 (RunID, Seq) 重复调用**幂等**，不得产生第二份记录；
//   - Load 无记录时返回 ErrNotFound，不得返回 (零值, nil)；
//   - 并发安全（编排可能从多个环节同时落库）。
//
// 接缝契约（宿主实现本接口；它是进度快照存取边界的**基础形态**）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：**宿主侧兑现**。本包生产代码的存储面是 StoreCAS /
// FencedStoreCAS（Committer 走那两张），它们的调用点在 SeqGate 的锁**之外**
// （闸只保护「取哪个序号」）；本接口是同一存取边界的基础形态——宿主适配层
// 与本包的契约用例按它实现。故回查不会自锁：宿主实现不得在方法内回调
// 本包的其它接缝（不得回调 Committer / SeqGate）——调用方正同步等着它返回，
// 回查即互相等待。
// no-blocking：**宿主侧兑现**。落库可能要走网络，「不阻塞」在此处的准确含义
// 是「**有界**」——必须承接 ctx 的取消/超时并在期限内返回，不得无限等待、
// 不得在持存储内部锁时等待别的回调。
// panic-isolated：**宿主侧兑现**——内核**不隔离**本接口的 panic（本包无
// recover，Committer 直接调存储），宿主实现须自行保证不 panic 或在实现内部
// recover 成 error。
type Store interface {
	Save(ctx context.Context, s Snapshot) error
	Load(ctx context.Context, runID string) (Snapshot, error)
}

// SeqGate 同一 RunID 的快照序号单调闸：拒绝回写不更新的快照。
//
// # 为什么需要它
//
// 真实故障模式不是「有人故意写旧快照」，而是**并发落库乱序**：编排按步骤并发推进，
// 第 N+1 步的快照先落库、第 N 步的慢写随后到达，把新状态覆盖成旧状态。
// 结果是恢复点静默回退——不报错，只在下一次崩溃时让已完成的步骤重跑。
// 这个 bug 极难定位，且**纯靠 Store 的幂等实现挡不住**（两次写都是合法的）。
//
// # 局限：它是进程内的，而本包的立论是崩溃恢复
//
// 零值不可用，请用 NewSeqGate。
//
// SeqGate 的记录只在进程内存里。**进程重启后它是空的**——此时上一进程写下的
// 「陈旧快照」（例如慢写终于落地的旧值）会被畅通无阻地写入，
// 正是本包要防的「恢复点静默回退」。
//
// 补救是宿主在重启后用 Store.Load 把最新快照灌进来：用 Seed。
// 不灌就等于放弃了这道闸——这不是「闸没生效」，是**闸被重置后没人告知**。
type SeqGate struct {
	mu   sync.Mutex
	last map[string]uint64
	// inflight 每个 RunID **已 Accept 但尚未 Release** 的序号（严格升序，
	// 末元素恒等于 last）。
	//
	// 为什么不是单格 prev：Accept → Store.Save → 失败 Release 是包文档公开的
	// 正规用法，而 Store 要求并发安全（多个环节可同时落库）。两次在途提交
	// 同时失败时，单格 prev 在第一次回滚后就丢掉更早的地板，第二次回滚要么
	// 把 last 停在「前一个在途值」上（它同样没落库成功），要么把地板跌到 0。
	// 前者的后果是该序号的重试被 ErrStaleSeq **永久拒**——恢复点不是回退，
	// 是整个 run 卡死（issue #216）；后者会让陈旧慢写重新获得通行权。
	//
	// 记录的是「已接受、调用方尚未宣布失败」的序号，故地板 = 其中最大值 =
	// 末元素。Release 只移除对应格（回滚顺序任意），last 回落到剩余末元素；
	// 全部移除即删掉该 RunID 的记录（本来就没有地板）。
	inflight map[string][]uint64
}

// NewSeqGate 构造单调闸。
func NewSeqGate() *SeqGate {
	return &SeqGate{last: make(map[string]uint64), inflight: make(map[string][]uint64)}
}

// Seed 用**已持久化的**快照初始化序号基线。宿主重启后必须调用一次，
// 做法是 Store.Load 拿到最新快照后灌进来。
//
// seq <= 0 视为无基线（直接忽略），避免用「零快照」把闸误设成已有记录。
// 幂等：以同一个快照重复 Seed 结果相同。
func (g *SeqGate) Seed(runID string, seq uint64) {
	if runID == "" || seq == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if cur, ok := g.last[runID]; !ok || cur < seq {
		g.last[runID] = seq
		// 基线被持久化事实抬高后，旧的在途格不再是合法回滚目标（宿主重启场景下
		// 内存里的历史本就不可信），整批作废，免得日后 Release 退回到一个陈旧地板。
		delete(g.inflight, runID)
	}
}

// Accept 校验并记录快照序号。
//
// 首个快照（seq > 0 且无历史）恒通过；此后要求 seq **严格大于**该 RunID 的历史最大值。
// 返回 ErrStaleSeq 时**不记录**，调用方应丢弃该次落库。
//
// # 调用顺序（重要）
//
// Accept 只保证「不倒退」，它**不保证「已落库」**——落库成败由 Store 决定。
// 因此正确顺序是 Accept → Store.Save，**且 Save 失败时必须调 Release 回滚**：
//
//	gate.Accept(s)   // 挡住并发的陈旧写
//	if err := store.Save(ctx, s); err != nil {
//	    gate.Release(s.RunID, s.Seq) // 不回滚的话闸会跑到实际存储前面，
//	    return err                    // 同序号重试将被判为陈旧而永远写不进去
//	}
//
// 不回滚的真实后果不是「多花一个序号」，而是**这个 run 从此卡死**：闸认为
// seq=N 已落库，实际没有；重试 N 被拒、跳过 N 又留下一条永远补不回来的空洞。
func (g *SeqGate) Accept(s Snapshot) error {
	if s.Seq == 0 {
		return nil // 未初始化序号，不参与单调判定
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if prev, ok := g.last[s.RunID]; ok && s.Seq <= prev {
		return ErrStaleSeq
	}
	// 记下本次在途序号供 Release 精确回滚。严格升序由上面的判定保证：
	// seq > last == 末尾元素，故 append 后仍升序。
	g.inflight[s.RunID] = append(g.inflight[s.RunID], s.Seq)
	g.last[s.RunID] = s.Seq
	return nil
}

// Last 返回该 RunID 已接受的最大序号；无记录返回 0。
func (g *SeqGate) Last(runID string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last[runID]
}

// Release 回滚一次已接受但**未成功落库**的序号，使同序号可以重试。
//
// 返回 true 表示 seq 是当前最大值且地板已回落到剩下的最大在途值（全无则删记录）；
// 返回 false 表示该序号**不是当前最大值**（已被更高序号取代），或从未 Accept /
// 已被重复回滚。
//
// # 任意回滚顺序都成立（issue #216）
//
// 在途序号按 RunID 记**多格**（升序切片），Release 只移除 seq 对应那一格；
// 它是否当前最大值只决定返回值与 last 是否回落。高于 seq 的在途格不受影响，
// 低于 seq 的在途格也不会被误当新地板——所以两次并发失败无论先回滚谁，
// 最终地板都回到「最后一次成功落库的序号」，两次重试都能通过。
//
// 被取代的 seq 回滚时虽然返回 false（当前最大值未变），它的在途格**仍会被移除**：
// 调用方说它没落库成功，日后更高序号也失败回滚时就不能把地板停在它上面
// ——那正是单格 prev 把 run 卡死的形态。重复回滚同一序号是正常形态，no-op。
//
// # 回滚到已落库地板，不是清零
//
// 这里退回到剩余的**最大在途值**（即最后一次 Accept 而调用方尚未宣布失败的
// 序号），而不是把记录删掉：删记录会把上一个已成功落库的序号一起抹掉，闸的
// 地板从 5 跌到「无」，于是 seq=3 的陈旧慢写畅通无阻——恢复点静默回退，
// 正是本闸要防的那件事。只有在没有剩余在途值时（本来就没有地板）才删记录。
func (g *SeqGate) Release(runID string, seq uint64) bool {
	if seq == 0 {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	pending, ok := g.inflight[runID]
	if !ok {
		return false
	}
	idx := -1
	for i, v := range pending {
		if v == seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false // 未在途（未知 / 重复回滚）
	}
	wasMax := g.last[runID] == seq
	pending = append(pending[:idx], pending[idx+1:]...)
	if len(pending) == 0 {
		delete(g.inflight, runID)
		delete(g.last, runID) // 本就无地板，退回「无记录」
		return wasMax
	}
	g.inflight[runID] = pending
	if wasMax {
		// 剩余元素严格小于 seq，故末元素就是回落后仍成立的最大在途值。
		g.last[runID] = pending[len(pending)-1]
	}
	return wasMax
}
