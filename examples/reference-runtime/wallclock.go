package main

// wallclock.go —— 会话级墙钟的接缝（deadline.Store 的内存实现）与运行时的
// 墙钟接线（issue #355）。
//
// # 为什么参考实现要接墙钟
//
// 会话往往跨多轮（自动续跑/排队续跑）：**总时限不能因续跑轮重置**——每次续跑
// 都新开一段时限的话，会话可以无限续跑下去。这条契约由 deadline 的锚点机制
// 保证（首轮写一次、续跑轮沿用），而宿主照抄本参考实现时，接线长什么样就是
// 什么——故这里把它接出来（墙钟是**可选**的：零值 Config 是关闭直通，行为与
// 从前逐字相同）。
//
// 锚点存哪由宿主决定（DB 列 / 内存表 / 文件）；本示例用内存版，与参考实现的
// 内存存储同款：进程内跨轮可见，进程重启后由宿主从自己的存储恢复。

import (
	"context"
	"sync"

	"github.com/yi-nology/goagent-kernel/deadline"
)

// wallClockStore 锚点持久化的接缝（deadline.Store）：参考实现的内存版。
//
// 语义与真实存储一致：首轮写一次、续跑轮读同一份（**绝不刷新**——刷新等于把
// 总时限重新计时）。
type wallClockStore struct {
	mu     sync.Mutex
	anchor *deadline.Anchor
}

// LoadAnchor 读锚点；**未写必须返回 (nil, nil)**。
//
// 返回零值锚点会被判成「已写但过期」（Anchor.State 的口径），续跑轮会当场
// timed_out——那是谎报，不是保守。
func (s *wallClockStore) LoadAnchor(_ context.Context) (*deadline.Anchor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.anchor == nil {
		return nil, nil
	}
	out := *s.anchor
	return &out, nil
}

// SaveAnchor 写锚点（只在该写时被调用一次：首轮）。
func (s *wallClockStore) SaveAnchor(_ context.Context, a deadline.Anchor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := a
	s.anchor = &out
	return nil
}

// newWallClockStore 造一个空的锚点存储（未写状态）。
func newWallClockStore() *wallClockStore { return &wallClockStore{} }
