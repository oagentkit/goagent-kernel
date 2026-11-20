package roundguard

import "sync"

// Meter 进程内回合账（只数不判）。
//
// **进程内**：跨进程续跑时回合数**不随之累计**——跨进程累计需要存储形态，那是宿主
// 资产；要跨进程累计的宿主须自行落库并把 Used 灌回本包。
//
// **并发安全**：带锁。判据包不该把加锁方式留给调用方各自发明——宿主各写一遍加锁
// 方式 = 同一种并发缺陷的 N 个变体。
type Meter struct {
	mu      sync.Mutex
	rounds  int
	calls   int
	perTool map[string]int
}

// NewMeter 构造一个空的回合账。
func NewMeter() *Meter {
	return &Meter{perTool: make(map[string]int)}
}

// Round 记一回合并返回累计回合数。
func (m *Meter) Round() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rounds++
	return m.rounds
}

// Tool 记一次工具调用并返回该工具的累计调用数。
func (m *Meter) Tool(toolID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.perTool[toolID]++
	return m.perTool[toolID]
}

// Used 导出用量快照（副本——调用方改写它不影响记账）。
func (m *Meter) Used() Used {
	m.mu.Lock()
	defer m.mu.Unlock()
	tools := make(map[string]int, len(m.perTool))
	for k, v := range m.perTool {
		tools[k] = v
	}
	return Used{Rounds: m.rounds, ToolCalls: m.calls, Tools: tools}
}
