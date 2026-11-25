// invocationtest_test.go —— 用一份内存实现把套件跑绿（正例）；
// 变异实现与判据的牙齿验证在 mutation_test.go。
package invocationtest

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/oagentkit/goagent-kernel/invocation"
)

// memStore 一份最小的内存实现（仅供套件自证；生产实现归宿主）。
//
// ★ 它的 Advance **在一次持锁内完成校验与写入**——这正是宿主实现必须做到的
// 形态（读-判-写不可分割）。拆成三步的那份在 mutation_test.go。
type memStore struct {
	mu   sync.Mutex
	rows map[string]invocation.Entry
	seq  uint64
}

// rawMemStore 造一份空的内存实现（供变异实现包一层用）。
func rawMemStore() *memStore {
	return &memStore{rows: map[string]invocation.Entry{}}
}

// newMemStore 套件的工厂（每个子测试一份全新实现）。
func newMemStore(_ *testing.T) invocation.Store {
	return rawMemStore()
}

// rowKey 账目的存储键（会话 + 调用）。
func rowKey(sessionID, callID string) string {
	return sessionID + "\x00" + callID
}

// Plan 落一条账目（首次登记身份；同身份重放幂等；异身份拒绝）。
func (m *memStore) Plan(_ context.Context, e invocation.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := rowKey(e.SessionID, e.CallID)
	if prev, ok := m.rows[k]; ok {
		if prev.Tool != e.Tool || prev.ArgsDigest != e.ArgsDigest {
			return invocation.ErrIdentityConflict
		}
		return nil // 幂等：恢复重放同一计划不报错
	}
	m.seq++
	e.Seq = m.seq
	m.rows[k] = e
	return nil
}

// Get 取一条账目。
func (m *memStore) Get(_ context.Context, sessionID, callID string) (invocation.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.rows[rowKey(sessionID, callID)]
	if !ok {
		return invocation.Entry{}, invocation.ErrNotFound
	}
	return e, nil
}

// List 列出会话的全部账目（按序号升序）。
func (m *memStore) List(_ context.Context, sessionID string) ([]invocation.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]invocation.Entry, 0, len(m.rows))
	for _, e := range m.rows {
		if e.SessionID == sessionID {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b invocation.Entry) int {
		return cmp.Compare(a.Seq, b.Seq)
	})
	return out, nil
}

// Advance 推进账目（校验与写入在**同一次持锁**内完成 ⇒ 原子）。
func (m *memStore) Advance(_ context.Context, sessionID, callID string, to invocation.State, u invocation.Update) (invocation.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := rowKey(sessionID, callID)
	cur, ok := m.rows[k]
	if !ok {
		return invocation.Entry{}, invocation.ErrNotFound
	}
	if cur.State == to {
		return cur, nil // 幂等：推进到当前状态不报错
	}
	if !invocation.CanTransition(cur.State, to) {
		return invocation.Entry{}, invocation.ErrIllegalTransition
	}
	cur.State = to
	if len(u.Result) > 0 {
		cur.Result = u.Result
	}
	if u.Reason != "" {
		cur.Reason = u.Reason
	}
	m.rows[k] = cur
	return cur, nil
}

// TestConformanceOnMemoryStore 好实现必须全绿。
func TestConformanceOnMemoryStore(t *testing.T) {
	Run(t, newMemStore)
}
