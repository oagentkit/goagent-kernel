// state.go —— 事务域的**已提交状态**（重放后的真相）。
//
// 四个面（任务板 / 副作用闸 / 进度快照 / 事件行）共享这一份状态：它们不是
// 四份独立数据，而是**同一份事务日志重放出来的四个投影**。⇒ 「共用一份
// 事务域」不是约定，是结构——状态里根本没有「只更新了其中一面」这回事。
//
// 状态只从日志重建（replay），不在内存里长期累积：每次事务都从日志重放
// 一次，判定读的是重放后的最新事实（崩溃不会让「谁占着任务」出现两个
// 答案）。
package sqlitestore

import (
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// taskEntry 一个任务的当前状态（任务面）。
type taskEntry struct {
	Task  taskboard.Task
	Fence uint64
}

// actEntry 一个幂等键的状态（副作用面 · Key 级：这个动作做过没有）。
type actEntry struct {
	Done      bool              `json:"done"`
	Result    sideeffect.Result `json:"result"`
	Uncertain bool              `json:"uncertain"`
	Reason    string            `json:"reason,omitempty"`

	// 动作身份三元组（首次 Acquire 登记即永久事实，见 ErrActionConflict）。
	IDSet         bool                   `json:"id_set"`
	IDResource    string                 `json:"id_resource"`
	IDSafety      sideeffect.RetrySafety `json:"id_safety"`
	IDPayloadHash string                 `json:"id_payload_hash"`

	Intent *intentEntry `json:"intent,omitempty"`
	Recon  *reconEntry  `json:"recon,omitempty"`
}

// intentEntry 一次尚未裁决的执行意图。
type intentEntry struct {
	Resource string           `json:"resource"`
	Fence    sideeffect.Fence `json:"fence"`
	Holder   string           `json:"holder"`
	Expires  time.Time        `json:"expires"`
}

// reconEntry 一次对账认领（对账权占用）。
type reconEntry struct {
	Holder  string    `json:"holder"`
	Expires time.Time `json:"expires"`
}

// resEntry 一个资源的独占持有与栅栏水位（副作用面 · 资源级：闸）。
type resEntry struct {
	Fence  sideeffect.Fence `json:"fence"`
	Holder string           `json:"holder"`
	Until  time.Time        `json:"until"`
}

// effectState 副作用面的两份状态：Key 级（幂等）与资源级（闸）。
//
// 两级不共享状态但同处一份事务域——判据要求「一次原子操作里同时决定」，
// 落库形态的做法是：两份状态在**同一条日志记录**里一起变。
type effectState struct {
	acts map[effectKey]*actEntry
	ress map[effectKey]*resEntry
}

// effectKey 副作用面记录的定位键（租户 + 幂等键 / 资源标识）。
//
// 租户是**身份第二维**：跨租户同名动作互不命中（Action.Tenant 的语义）。
// 与 examples/pgstore 的 (tenant, key) 联合定位同一口径——两边若不一致，
// 同一份宿主代码换个存储就得到不同的去重结果。
type effectKey struct {
	Tenant string
	ID     string
}

// actKeyOf 动作的定位键。
func actKeyOf(act sideeffect.Action) effectKey {
	return effectKey{Tenant: act.Tenant, ID: string(act.Key)}
}

// resKeyOf 资源闸的定位键。
func resKeyOf(act sideeffect.Action) effectKey {
	return effectKey{Tenant: act.Tenant, ID: act.Resource}
}

// state 一次事务读到的已提交状态（四个面共享）。
type state struct {
	tasks     map[taskboard.TaskID]*taskEntry
	effects   effectState
	snapshots map[string]*checkpoint.Snapshot
	events    []eventRecord
	// watermark 已提交水位线：≤ 此值的事件 ID 已全部提交且可见。
	// 事件 ID 由它派生 ⇒ 铸造序 == 提交序，回滚不烧号 ⇒ 无空洞推进。
	watermark uint64
}

// newState 造一份空状态。
func newState() *state {
	return &state{
		tasks: map[taskboard.TaskID]*taskEntry{},
		effects: effectState{
			acts: map[effectKey]*actEntry{},
			ress: map[effectKey]*resEntry{},
		},
		snapshots: map[string]*checkpoint.Snapshot{},
	}
}

// actCopy 取幂等键的状态**副本**（判定在副本上做，落库时才安装）。
func (s *state) actCopy(key effectKey) *actEntry {
	if e, ok := s.effects.acts[key]; ok {
		return cloneAct(e)
	}
	return &actEntry{}
}

// resCopy 取资源闸的副本。
func (s *state) resCopy(resource effectKey) *resEntry {
	if e, ok := s.effects.ress[resource]; ok {
		return cloneRes(e)
	}
	return &resEntry{}
}

// applyRecord 把一条日志记录应用到状态上。
//
// **重放与运行时安装走同一条路径**（纪律）：重放时怎么算的，运行时就得
// 怎么算——两套算法必然漂移，而漂移的形态是「重启后状态变了」。
func applyRecord(st *state, rec journalRecord) error {
	switch rec.Kind {
	case kindTask:
		if rec.Task == nil || rec.Task.ID == "" {
			return oerrors.New("sqlitestore: 日志记录缺任务 id")
		}
		st.tasks[rec.Task.ID] = &taskEntry{Task: cloneTask(*rec.Task), Fence: rec.Fence}
	case kindEffect:
		if rec.Effect == nil {
			return oerrors.New("sqlitestore: 副作用记录缺载荷")
		}
		if rec.Effect.Act != nil {
			key := effectKey{Tenant: rec.Effect.Tenant, ID: rec.Effect.Key}
			st.effects.acts[key] = cloneAct(rec.Effect.Act)
		}
		if rec.Effect.Res != nil && rec.Effect.ResKey != "" {
			key := effectKey{Tenant: rec.Effect.Tenant, ID: rec.Effect.ResKey}
			st.effects.ress[key] = cloneRes(rec.Effect.Res)
		}
	case kindSnapshot:
		if rec.Snapshot == nil {
			return oerrors.New("sqlitestore: 快照记录缺载荷")
		}
		st.snapshots[rec.Snapshot.RunID] = cloneSnapshot(rec.Snapshot.Snapshot)
	case kindEvent:
		if rec.Event == nil {
			return oerrors.New("sqlitestore: 事件记录缺载荷")
		}
		// 无空洞推进的**机器形态**：事件 ID 必须连续。日志里出现空洞 ⇒
		// 有人绕过写入协议落了行（或日志被外部改写）⇒ 响亮失败，
		// 绝不静默跳号（跳号 = 永久丢帧）。
		if rec.Event.ID != st.watermark+1 {
			return oerrors.Wrap(oerrors.New("sqlitestore: 事件游标出现空洞"),
				"sqlitestore: 事件 ID 必须连续（铸造序 == 提交序，回滚不烧号）",
				"期望 id", st.watermark+1, "实得 id", rec.Event.ID)
		}
		st.events = append(st.events, *rec.Event)
		st.watermark = rec.Event.ID
	default:
		return oerrors.Wrap(oerrors.New("sqlitestore: 未知记录类型"),
			"sqlitestore: 日志记录类型不可识别", "kind", rec.Kind)
	}
	return nil
}

// cloneAct 深拷贝幂等键状态（载荷与指针各换一份）。
func cloneAct(in *actEntry) *actEntry {
	if in == nil {
		return &actEntry{}
	}
	out := *in
	out.Result.Payload = append([]byte(nil), in.Result.Payload...)
	if in.Intent != nil {
		it := *in.Intent
		out.Intent = &it
	}
	if in.Recon != nil {
		rc := *in.Recon
		out.Recon = &rc
	}
	return &out
}

// cloneRes 深拷贝资源闸状态。
func cloneRes(in *resEntry) *resEntry {
	if in == nil {
		return &resEntry{}
	}
	out := *in
	return &out
}

// cloneSnapshot 深拷贝进度快照（切片与开放键各换一份）。
func cloneSnapshot(in checkpoint.Snapshot) *checkpoint.Snapshot {
	out := in.Clone()
	return &out
}
