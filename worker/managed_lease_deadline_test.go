// managed_lease_deadline_test.go —— 持续存储故障下的**本地资格截止**判据
//（P0-B：不依赖「下一拍必然拿到 ErrLeaseLost 回包」这个在持续网络分区下
// 不成立的隐含前提）。
//
// 正对照：续期持续报错 + 本地时钟推过「最近确认 + TTL − margin」
// ⇒ 会话必须主动失格（ctx 取消）。
// 负对照：同样持续报错、时钟未推过截止 ⇒ 不得失格（自愈语义仍在窗口内）。
package worker

import (
	"context"
	"testing"
	"time"

	kerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// startFlakySession 认领任务后起会话，并立刻注入「存储持续不可达」。
func startFlakySession(t *testing.T, clock *testClock, board *flakyBoard) LeaseSession {
	t.Helper()
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	task, err := board.Claim(context.Background(), "t1", "w-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewLeaseSession(context.Background(), board, task, LeaseSessionConfig{
		TTL:           time.Minute,
		RenewInterval: time.Millisecond * 50,
		Now:           clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	board.failRenew.Store(true) // 认领成功后存储持续不可达
	return session
}

func TestLeaseSessionLosesWhenStoreUnreachablePastDeadline(t *testing.T) {
	clock := newTestClock()
	board := &flakyBoard{BoardAdapter: newTestBoard(clock)}
	session := startFlakySession(t, clock, board)
	defer func() { _ = session.Close(context.Background()) }()

	// 持续故障若干拍（时钟未动：仍在资格窗口内，不得失格）。
	time.Sleep(200 * time.Millisecond)
	select {
	case <-session.Context().Done():
		t.Fatal("资格窗口内的一拍失败不得失格（自愈语义）")
	default:
	}

	// 时钟推过「最近确认 + TTL − TTL/10」：存储侧租约早已过期，
	// 必须主动撤销执行资格——不等存储回包。
	clock.Advance(time.Minute)
	select {
	case <-session.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("持续故障超过本地资格截止必须主动失格（不等 ErrLeaseLost 回包）")
	}
	if err := session.AssertOwned(context.Background()); !kerrors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("失格后必须快速失败（ErrLeaseLost 链）：%v", err)
	}
}

// TestLeaseSessionSurvivesTransientFailuresWithinDeadline 负对照：故障但总时长
// 未过资格截止 ⇒ 不失格。
func TestLeaseSessionSurvivesTransientFailuresWithinDeadline(t *testing.T) {
	clock := newTestClock()
	board := &flakyBoard{BoardAdapter: newTestBoard(clock)}
	session := startFlakySession(t, clock, board)
	defer func() { _ = session.Close(context.Background()) }()

	// 故障 200ms（时钟不动），随后时钟推进 30s（< TTL−margin），再故障。
	time.Sleep(200 * time.Millisecond)
	clock.Advance(30 * time.Second)
	time.Sleep(200 * time.Millisecond)

	select {
	case <-session.Context().Done():
		t.Fatal("未过本地资格截止不得失格")
	default:
	}
}
