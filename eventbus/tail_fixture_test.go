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

// readAttempts 返回 ListAfter 被调用过的次数（**读拍数**）——线程安全。
//
// 它是「机制确实跑过 N 拍」这条尺子的来源：尾随的每一拍 = 一次读。
// 读拍数是**可观测事实**：与机器负载无关（负载越重拍越多，只是慢，不是少）。
func (s *fakeTailStore) readAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.listCalls)
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

// tailAbsenceReadBeats 缺席断言要求的**读拍数**（覆盖力口径，详见
// waitTailReadBeats 与 assertNoTailFrame）。
//
// 取 20 拍的来由：旧的墙钟窗口是 50ms，在 10ms 节拍下约等于 5 拍 ⇒ 只给了
// 被测机制**前 5 拍**做坏事的机会；20 拍覆盖窗口之外 4 倍的地面——坏实现
// 只要在自己的某一拍里回灌存量，就跑不掉（与负载无关：负载越重拍越多，
// 只是慢，不是少）。
const tailAbsenceReadBeats = 20

// waitTailReadBeats 等尾随**读拍**跑满 n 拍（1 拍 = 一次 ListAfter 读）。
//
// # 尺子必须是可观测事实，不是墙钟窗口（#432 / #443）
//
// 旧写法是「等某个事实成立 → 睡一段墙钟 → 判窗口里有没有事件」，两个毛病：
// 窗口落空（窗口内机制还没跑到那一步）⇒ 什么都没验却绿；证据落空（坏实现
// 慢一点才做坏事）⇒ 守卫抓不住它声称要拦的东西。
//
// 这里改成「等夹具数出来的读拍数 ≥ n」：拍数是**事实**，与负载无关。
// 墙钟（2s）只作**兜底上限**——防挂死，不参与结论：拍数没跑满就是
// 「机制压根没跑够」⇒ 此时判缺席等于什么都没验 ⇒ 直接红，不写成绿。
//
// # 为什么多等一次读（「跑满 n 拍」= 读到第 n+1 次）
//
// 拍内顺序是「读 → 回灌 → 下一拍」：第 n+1 次读被调用 ⇒ 第 n 拍的**回灌阶段
// 已经跑完**（happens-before 经夹具的同一把互斥锁传递：读拍计数与读游标序列
// 都在 store.mu 之下）⇒ 「此刻通道里有什么」是确定的 ⇒ 后面那条缺席断言
// 不是在跟竞态赛跑。
// tailReadCounter 读拍计数的最小能力面——fakeTailStore 之外，
// lateCommitStore（tail_cursor_gap_test）也自带计数，同一把尺子量两种夹具。
type tailReadCounter interface {
	readAttempts() int
}

func waitTailReadBeats(t *testing.T, store tailReadCounter, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for store.readAttempts() < n+1 {
		if time.Now().After(deadline) {
			t.Fatalf("2s 内尾随读拍未跑满 %d 拍（实得 %d 拍）——机制没跑够拍，"+
				"缺席断言没有证据价值（此刻判绿等于什么都没验）", n, store.readAttempts())
		}
		time.Sleep(time.Millisecond)
	}
}

// assertNoTailFrame 判「在机制确实跑过 n 读拍的前提下，通道里仍无事件」。
//
// 必须是**非阻塞** drain：此刻通道内容已由 waitTailReadBeats 的 happens-before
// 关系钉死（见该助手注释）⇒ 有事件就是「机制跑够了拍还吐了事件」⇒ 立刻红
// （失败信息带上跑过的拍数，读者一眼能分「是没跑够」还是「跑够了还吐」）；
// 没事件就是缺席成立。
//
// 若写成「再等一段窗口」，尺子又退化成墙钟（#432：窗口落空 ⇒ 假红、
// 证据落空 ⇒ 白测）——那正是本单要清偿的形态。
func assertNoTailFrame(t *testing.T, ch <-chan Event, n int) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("机制已跑 %d 读拍却仍收到事件（本面是补灌不是回放）: %+v", n, ev)
	default:
	}
}
