package eventbus

// event-tail 跨副本补灌面（issue #166）的**夹具与共享助手**。
//
// 本文件守住的是「替身语义」这一块契约：
//
//   - fakeTailStore：进程内 TailStore 替身。它同时是本包的**可观测面**
//     （读拍次数 / 读游标序列 / MaxID 调用次数）——「机制确实跑过 N 拍」这类
//     断言要有尺子，尺子就是它数出来的拍数（见 waitTailReadBeats）。
//   - errFakeRead / errTailFake：读失败替身错误（合成数据，不入
//     docs/error-semantics.md——只在 _test.go 内）。
//   - waitTail：等订阅通道给出下一帧（失败即 t.Fatal）。
//
// # 为什么夹具单独成文件（#443）
//
// 原 eventbus/tail_test.go 379/400 行，逼近结构门禁 max_file_lines ⇒ 按关注点
// 拆成五块：夹具/助手（本文件）、补达与初始化（tail_delivery_test.go）、
// 读失败与幂等（tail_failure_test.go）、取消与泄漏（tail_cancel_test.go）、
// 装配校验（tail_assembly_test.go）。夹具与共享助手**只此一份**，其余文件
// 按包内符号直接引用（不复制、不改名）。

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeTailStore 进程内 TailStore 替身（并发安全：Run 在测试 goroutine 外跑）。
type fakeTailStore struct {
	mu sync.Mutex

	maxID      uint64
	evs        []Event // 按 ID 升序
	failMaxID  int     // 前 N 次 MaxID 返回错误
	failList   int     // 前 N 次 ListAfter 返回错误
	maxIDCalls int
	listCalls  []uint64 // 被请求的游标序列
}

func (s *fakeTailStore) MaxID(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxIDCalls++
	if s.failMaxID > 0 {
		s.failMaxID--
		return 0, errFakeRead
	}
	return s.maxID, nil
}

func (s *fakeTailStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls = append(s.listCalls, cursor)
	if s.failList > 0 {
		s.failList--
		return nil, errFakeRead
	}
	var out []Event
	for _, ev := range s.evs {
		if ev.ID <= cursor {
			continue
		}
		out = append(out, ev)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *fakeTailStore) append(evs ...Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evs = append(s.evs, evs...)
	for i := len(s.evs) - 1; i > 0; i-- { // 保序（测试插入无序也按升序契约回出）
		if s.evs[i].ID < s.evs[i-1].ID {
			s.evs[i], s.evs[i-1] = s.evs[i-1], s.evs[i]
		}
	}
}

func (s *fakeTailStore) cursors() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint64(nil), s.listCalls...)
}

func (s *fakeTailStore) maxIDs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxIDCalls
}

// errFakeRead 读失败替身错误。
var errFakeRead = errTailFake("存储瞬时抖动")

// errTailFake 测试用哨兵（合成数据，不入 docs/error-semantics.md——只在 _test.go 内）。
type errTailFake string

func (e errTailFake) Error() string { return string(e) }

// waitTail 等订阅通道给出下一帧（失败即 t.Fatal）。
func waitTail(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到尾随帧")
		return Event{}
	}
}
