// loop_config_test.go —— NewLoop 的装配期校验与缺省值（issue #266 的续期旋钮）。
package worker

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

func TestNewLoopValidatesConfig(t *testing.T) {
	clock := newTestClock()
	board := newTestBoard(clock)
	reg := newMemRegistry(clock.Now)
	handler := HandlerFunc(func(context.Context, taskboard.Task) error { return nil })

	cases := []struct {
		name string
		cfg  Config
	}{
		{"缺 Agent", Config{TTL: time.Minute}},
		{"零 TTL", Config{Agent: "w-1"}},
		{"心跳不短于 TTL", Config{Agent: "w-1", TTL: time.Minute, HeartbeatInterval: 2 * time.Minute}},
		{"负并发度", Config{Agent: "w-1", TTL: time.Minute, Concurrency: -1}},
		{"续期余量不小于 TTL", Config{Agent: "w-1", TTL: time.Minute, RenewMargin: time.Minute}},
		{"负续期并发", Config{Agent: "w-1", TTL: time.Minute, RenewConcurrency: -1}},
		{"负续期超时", Config{Agent: "w-1", TTL: time.Minute, RenewTimeout: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLoop(board, reg, handler, tc.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("必须报 ErrInvalidConfig（实得 %v）", err)
			}
		})
	}
	if _, err := NewLoop(nil, reg, handler, Config{Agent: "w-1", TTL: time.Minute}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("缺任务板必须报 ErrInvalidConfig（实得 %v）", err)
	}
	// 缺省值补齐：PollInterval 0 → 缺省；HeartbeatInterval 0 → TTL/3；
	// Concurrency 0 → 1（既有串行行为逐字不变）。
	loop, err := NewLoop(board, reg, handler, Config{Agent: "w-1", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if loop.cfg.PollInterval != DefaultPollInterval || loop.cfg.HeartbeatInterval != 20*time.Second {
		t.Fatalf("缺省值未补齐：poll=%v heartbeat=%v", loop.cfg.PollInterval, loop.cfg.HeartbeatInterval)
	}
	if loop.cfg.Concurrency != 1 {
		t.Fatalf("并发度零值必须归一为 1（实得 %d）——缺省串行是兼容承诺", loop.cfg.Concurrency)
	}
	if loop.cfg.RenewMargin != 20*time.Second || loop.cfg.RenewConcurrency != DefaultRenewConcurrency ||
		loop.cfg.RenewTimeout != 15*time.Second {
		t.Fatalf("续期缺省值未补齐：margin=%v concurrency=%d timeout=%v",
			loop.cfg.RenewMargin, loop.cfg.RenewConcurrency, loop.cfg.RenewTimeout)
	}
}
