// mutation_test.go —— 变异实现 + 判据的牙齿验证。
//
// 套件的红必须有证据：关键判据配一个精确命中它的变异实现，摘掉判据必红；
// 「好实现绿」的那一半由 Run 在 memStore 上闭合（同一批判据）——成对验证缺
// 一边等于没验证。
//
//	nonAtomicStore → 推进的校验与写入分离（读-判-写三段，两个执行者同时通过）
package invocationtest

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/oekit/concurrency/async"

	"github.com/oagentkit/goagent-kernel/invocation"
)

// nonAtomicStore 把 Advance 拆成「读 → 判 → 写」三段（校验与写入分离）。
//
// 这正是「没做到原子」的真实形态：两个执行者同时读到 executing、同时判
// 通过、同时写 ⇒ 末态由**写入顺序**决定 ⇒ 同一事实两份真相。
type nonAtomicStore struct {
	*memStore
	// barrier 在「判」与「写」之间对齐并发推进者。
	//
	// 为什么是**可装卸**的：它只该在并发那一段生效——单发推进若也等着，
	// 就没有第二个推进者来对齐 ⇒ 死锁（那是夹具自己的形态，不是判据要抓的
	// 形态）。装卸走原子指针：并发推进者读它时不与装卸动作竞争。
	barrier atomic.Pointer[sync.WaitGroup]
}

// newNonAtomicStore 造一份非原子实现。
func newNonAtomicStore(_ *testing.T) invocation.Store {
	return &nonAtomicStore{memStore: rawMemStore()}
}

// arm 让接下来的 n 个推进在「判」与「写」之间对齐。
func (s *nonAtomicStore) arm(n int) {
	wg := &sync.WaitGroup{}
	wg.Add(n)
	s.barrier.Store(wg)
}

// disarm 撤掉对齐（并发段之后调用，免得下一次单发推进又等着）。
func (s *nonAtomicStore) disarm() {
	s.barrier.Store(nil)
}

// Advance 非原子推进：读与写之间有窗口。
func (s *nonAtomicStore) Advance(ctx context.Context, sessionID, callID string, to invocation.State, u invocation.Update) (invocation.Entry, error) {
	// 第一段：读（拿完锁就放开）。
	s.mu.Lock()
	cur, ok := s.rows[rowKey(sessionID, callID)]
	s.mu.Unlock()
	if !ok {
		return invocation.Entry{}, invocation.ErrNotFound
	}
	if cur.State == to {
		return cur, nil
	}
	if !invocation.CanTransition(cur.State, to) {
		return invocation.Entry{}, invocation.ErrIllegalTransition
	}
	// 所有并发推进者在这里对齐：保证「判」都发生在「写」之前。
	if wg := s.barrier.Load(); wg != nil {
		wg.Done()
		wg.Wait()
	}
	// 第三段：写（各自凭自己读到的旧值决定）。
	s.mu.Lock()
	defer s.mu.Unlock()
	cur = s.rows[rowKey(sessionID, callID)]
	cur.State = to
	if len(u.Result) > 0 {
		cur.Result = u.Result
	}
	s.rows[rowKey(sessionID, callID)] = cur
	return cur, nil
}

// TestSuiteCatchesNonAtomicStore 并发推进判据的反向验证：不原子的实现必须
// 让两个执行者同时通过。
//
// 没有这一条，「恰好一个赢家」可能无论实现对错都成立（判据没有牙齿）；
// 这里直接对变异实现并发推进一次，断言**两个都成功**——正是套件那条
// 「恰好一个赢家」要抓住的形态。
func TestSuiteCatchesNonAtomicStore(t *testing.T) {
	st := newNonAtomicStore(t).(*nonAtomicStore)

	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	if err := st.Plan(context.Background(), e); err != nil {
		t.Fatalf("落账失败: %v", err)
	}
	if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateApproved, invocation.Update{}); err != nil {
		t.Fatalf("推进到 approved 失败: %v", err)
	}

	// 并发段才装上对齐（两个推进者各推一个**互斥**目标状态：executing 与
	// rejected——串行顺序下也互斥，故「两个都成功」只可能来自非原子实现）。
	st.arm(2)
	defer st.disarm()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		succ int
	)
	for _, to := range []invocation.State{invocation.StateExecuting, invocation.StateRejected} {
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			_, err := st.Advance(context.Background(), e.SessionID, e.CallID, to, invocation.Update{})
			if err == nil {
				mu.Lock()
				succ++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if succ != 2 {
		t.Fatalf("不原子的实现应让两个执行者都通过（got %d）——判据没有牙齿", succ)
	}
}
