package steer_test

// 写序纪律回归（issue #205）：终报只属于赢下条件迁移的那一路——先仲裁后写。
// 与 steer_test.go 的通用收尾用例分开成文件：本仓 max_file_lines 阈值判测试
// 文件，写序面单独成档也便于按 issue 定位。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/steer"
)

// TestRunSingleWinnerConcurrent 单协程迁移无双跑：并发投喂 N 次接管收尾，
// 条件迁移恰有一个赢家——输家零行静默（Status 空），且全程无数据竞争。
// 终报与状态同纪律（issue #205）：报告只属于赢家，N 路并发 ⇒ len(reports)==1
// ——输家先于仲裁写报告会把赢家终报覆写成相反结论。
func TestRunSingleWinnerConcurrent(t *testing.T) {
	st := &fakeStore{
		status: contract.StatusRunning,
		queue:  true,
		steps: []steer.DoneStep{
			{Slug: "step-a", Summary: "合成产出 A", Status: contract.StatusSucceeded},
		},
	}
	const n = 8
	outs := make([]steer.Outcome, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i], errs[i] = steer.Run(context.Background(), st, steer.Spec{Header: "## 已按新指令接管"})
		}(i)
	}
	wg.Wait()

	winners := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("第 %d 路报错: %v", i, errs[i])
		}
		if outs[i].Hit {
			winners++
			if outs[i].Status != contract.StatusCreated {
				t.Fatalf("赢家状态 = %q, want created", outs[i].Status)
			}
		} else if outs[i].Status != "" {
			t.Fatalf("输家不得报状态: %+v", outs[i])
		}
	}
	if winners != 1 {
		t.Fatalf("赢家 = %d, want 恰好 1（无双跑窗口）", winners)
	}
	if st.status != contract.StatusCreated {
		t.Fatalf("库内状态 = %q, want created", st.status)
	}
	if len(st.reports) != 1 {
		t.Fatalf("终报只属于赢家: len(reports)=%d, want 1（写序在仲裁前时输家会覆写）: %v",
			len(st.reports), st.reports)
	}
	if !strings.Contains(st.reports[0], "step-a：合成产出 A") {
		t.Fatalf("赢家终报必须是保产摘要:\n%s", st.reports[0])
	}
}

// TestRunZeroRowWritesNoReport 零行臂不产生任何 SetReport（issue #205 验收②）：
// 两条臂的零行都意味着他路已接手——输家既不动状态也不动终报。
func TestRunZeroRowWritesNoReport(t *testing.T) {
	// 回迁臂零行：库内不是 running。
	requeue := &fakeStore{status: contract.StatusCancelled, queue: true}
	out, err := steer.Run(steeredCtx(t), requeue, steer.Spec{Header: "## 头", FailReport: "## 失败"})
	if err != nil || out.Hit {
		t.Fatalf("回迁臂零行静默: out=%+v err=%v", out, err)
	}
	if len(requeue.reports) != 0 {
		t.Fatalf("回迁臂零行不得写报告: %v", requeue.reports)
	}

	// 失败臂零行：同面——失败报告也不落。
	fail := &fakeStore{status: contract.StatusCancelled, queue: false}
	out, err = steer.Run(steeredCtx(t), fail, steer.Spec{FailReport: "## 接管后中断"})
	if err != nil || out.Hit {
		t.Fatalf("失败臂零行静默: out=%+v err=%v", out, err)
	}
	if len(fail.reports) != 0 {
		t.Fatalf("失败臂零行不得写报告: %v", fail.reports)
	}
}

// TestRunMigrateErrorWritesNoReport 迁移出错（结论未知）时不得落报告：
// 不知道自己是不是赢家就动终报，等于可能覆写他路结论。
func TestRunMigrateErrorWritesNoReport(t *testing.T) {
	st := &fakeStore{
		status:     contract.StatusRunning,
		queue:      true,
		migrateErr: errors.New("存储不可达"),
	}
	if _, err := steer.Run(steeredCtx(t), st, steer.Spec{Header: "## 头"}); err == nil {
		t.Fatal("迁移失败必须报错")
	}
	if len(st.reports) != 0 {
		t.Fatalf("迁移出错不得写报告: %v", st.reports)
	}
}
