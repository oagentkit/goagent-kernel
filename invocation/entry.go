// entry.go —— 账目条目、身份摘要、错误哨兵与存储契约。
//
// 这些面与状态词表（invocation.go）分开：词表是**判定**面（取值决定结论），
// 这里是**形状**面（一条账目由哪些字段组成、宿主要实现哪些方法）。分开是
// 为了让两个文件各自讲一件事，也都留在 400 行以内。
package invocation

import (
	"context"

	"github.com/oekit/pkg/encoding"
	"github.com/oekit/pkg/errors"
)

// Entry 账本条目：一次工具调用的身份与当前状态。
//
// 身份三件套（SessionID + CallID + 工具名/参数摘要）**一经落账不可变**；
// State / Result / Reason 是可变的进度投影。
type Entry struct {
	// SessionID 长期会话身份（同一会话的多轮共用一个）。
	SessionID string
	// CallID 调用 ID（编排侧铸造）。
	//
	// ★ 恢复后**必须同 ID**：它是副作用幂等键的原材料——ID 变了，闸就认不出
	//   这是同一次动作 ⇒ 重复施加。
	CallID string
	// Tool 工具名（注册名）。
	Tool string
	// ArgsDigest 参数摘要（sha256 hex）——身份的一部分。
	ArgsDigest string
	// Args 参数原文（模型给出的 JSON 字节）。
	//
	// 落账时一并持久化：恢复时据此执行，**不必回头问模型要那次调用**。
	Args []byte
	// State 当前状态。
	State State
	// Result 结果字节（applied 后由执行方写入；result_recorded 后仍在账上，
	// 供对账与复盘取用）。
	Result []byte
	// Reason 原因说明（uncertain / rejected 时写）。
	Reason string
	// Seq 会话内单调序号（**存储侧铸造**）——批次内的执行顺序由它表达。
	Seq uint64
}

// Update 一次状态转移携带的数据。
type Update struct {
	// Result 结果字节（applied 时写）。
	Result []byte
	// Reason 原因说明（uncertain / rejected 时写）。
	Reason string
}

// 错误哨兵（宿主实现必须复用，不要各造一套：判定方按 errors.Is 认人）。
var (
	// ErrNotFound 账目不存在。
	ErrNotFound = errors.New("invocation: 账目不存在")
	// ErrIdentityConflict 同一 (会话, 调用) 的身份与首次登记不一致——
	// 那不是重试，是**另一个动作**（同款口径见 sideeffect.ErrActionConflict）。
	ErrIdentityConflict = errors.New("invocation: 同一调用的身份与首次登记不一致")
	// ErrIllegalTransition 状态转移不合法（跳态 / 从终态再动 / 词表外取值）。
	ErrIllegalTransition = errors.New("invocation: 非法的状态转移")
)

// Digest 参数摘要（sha256 hex）——身份的一部分。
//
// 算法单源在 oekit/pkg/encoding（与 sideeffect.HashPayload 同一算法口径）。
// 本包不复用 sideeffect 那个函数，是因为**账本与副作用闸是两个独立机制**
// （前者判调用身份与进度，后者判至多施加一次）——它们不该互相成为依赖。
func Digest(args []byte) string {
	return encoding.Sha256Hex(args)
}

// Store 账本存储契约（宿主实现；内核只判，不实现）。
//
// @seam: no-reentry no-blocking panic-caller
//
// 三条约束对宿主实现的含义：
//
//   - **不得重入**：实现里不得回调同一个 Store（自调用 ⇒ 自锁或递归推进）；
//   - **不得阻塞**：实现不得无限期阻塞（宿主据此给每次调用设超时）；
//   - **panic 由调用方处置**：实现 panic 时由调用方兜（内核不 recover——
//     recover 会把「账目被写坏」藏成一次普通失败）。
//
// 硬要求（宿主实现时必须满足，否则契约不成立）：
//
//   - **Plan 与 Advance 都是存储侧原子操作**（读-判-写不可分割）：
//     Plan 的「首次登记身份」与 Advance 的「当前状态允许该转移」都必须在
//     同一次原子判定里完成；
//   - **令牌/序号由存储侧铸造**（Entry.Seq），调用方自带序号等于没有序号；
//   - **失败形态是变红**：非法转移、身份冲突、状态未知，一律显式报错，
//     不静默放行、不静默改写。
type Store interface {
	// Plan 落一条待执行调用（身份一经落账不可变）。
	//
	// 幂等：同一 (SessionID, CallID) 且身份一致 ⇒ 幂等无操作（恢复重放同一
	// 计划时不得报错）；身份不一致 ⇒ ErrIdentityConflict。
	Plan(ctx context.Context, e Entry) error

	// Get 取一条账目；不存在 ⇒ ErrNotFound。
	Get(ctx context.Context, sessionID, callID string) (Entry, error)

	// List 列出会话的全部账目（按 Seq 升序）。
	List(ctx context.Context, sessionID string) ([]Entry, error)

	// Advance 把账目推进到目标状态（存储侧原子校验「当前状态允许该转移」）。
	//
	// 非法转移 ⇒ ErrIllegalTransition；目标状态已是当前状态 ⇒ 幂等无操作。
	Advance(ctx context.Context, sessionID, callID string, to State, u Update) (Entry, error)
}
