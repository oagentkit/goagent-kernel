// guard.go —— 闸的**装配面**：闸本体、构造与宿主注入的状态词表。
//
// 为什么单独成文件：commitgate.go 是本包的**判定与操作面**（Seed /
// SeedE / Acquire / ReleaseLease / Status / Committed），装配面混在里面时，
// 「宿主注入什么」与「内核判什么」读起来是一团。本文件只回答前者。
//
// 接缝声明（宿主注入的判据与存储）在本文件与 store.go 两处，各自带契约
// 标记——**逐条写明谁负责兑现**，不写空口承诺。
package commitgate

// Guard 终态独占提交闸。并发安全（状态操作全在 Store，实现各自保证）。
//
// 零值不可用，请用 NewGuard——闸的零值若表示「放行」，等于缺配置即失效。
type Guard struct {
	// store 已提交记录的存储接缝（缺省进程内参考实现）。
	store Store
	// isTerminal 由宿主注入的状态词表判定；nil 时按「全部都是终态」处理，
	// 这是更保守的一侧（宁可多拦，不可在不知道词表时漏拦）。
	isTerminal func(status string) bool
	// seedWarn 回灌失败留痕钩子（WithSeedWarn 装配期注入；nil 走包内 logx）。
	seedWarn SeedWarn
}

// NewGuard 构造闸（进程内参考存储）。isTerminal 判定某状态是否为终态；
// 传 nil 表示所有提交都按终态处理。
//
// 接缝契约（宿主注入的 isTerminal 谓词，内核在 Acquire / Seed 的判定路径上
// 调用它）：
//
// @seam: no-reentry no-blocking panic-isolated
//
//   - no-reentry：谓词是**纯判定**，不得回查本闸（不得在谓词里调 Acquire /
//     Seed / Status）——它跑在「判据 → 占用」的中间，回查会让判定与占用
//     互相嵌套。
//   - no-blocking：必须廉价，不得做 I/O（不得去问状态词表服务）——它在内核
//     的提交路径上，每次提交至少被问一次。
//   - panic-isolated：宿主侧兑现——本包**不做 recover**，谓词 panic 会沿
//     Acquire 上抛到调用方。
func NewGuard(isTerminal func(status string) bool) *Guard {
	return NewGuardWithStore(NewMemoryStore(), isTerminal)
}

// NewGuardWithStore 构造闸并注入存储接缝（issue #145）。
//
// 多副本宿主用它接自己的跨进程存储；判定逻辑仍只有本包这一份。
// s 为 nil ⇒ 退化为进程内参考存储。
//
// 接缝契约（宿主注入 isTerminal 谓词；s 是另一条接缝，其契约见 [Store]）：
//
// @seam: no-reentry no-blocking panic-isolated
//
//   - no-reentry：谓词是**纯判定**，不得回查本闸——它跑在「判据 → 占用」的
//     中间，回查会让判定与占用互相嵌套。存储实现同样不得回调本闸
//     （见 [Store] 的接缝契约）。
//   - no-blocking：谓词必须廉价，不得做 I/O。
//   - panic-isolated：宿主侧兑现——本包**不做 recover**。
func NewGuardWithStore(s Store, isTerminal func(status string) bool) *Guard {
	if s == nil {
		s = NewMemoryStore()
	}
	return &Guard{store: s, isTerminal: isTerminal}
}
