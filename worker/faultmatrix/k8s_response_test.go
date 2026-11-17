// k8s_response_test.go —— 矩阵行七：K8s API 响应丢失（Cluster）。
//
// 进程内模拟的是**执行幂等契约**（issue #263 的内核面）：Job 已建成但
// 创建响应丢失时，Worker 看到失败并重试；重试必须按**稳定执行标识**对账，
// 而不是重新生成标识再执行一份。真集群格（requiresEnv）另行声明，缺环境时
// 仍由本文件的可判事实兜底。
package faultmatrix

import (
	"sync"
	"testing"

	"github.com/oekit/pkg/errors"
)

var errInjectedResponseLost = errors.New("faultmatrix: 注入执行响应丢失（Job 已建、调用方超时）")

// executionLedger 执行标识账本：同标识的执行至多施加一次；响应丢失后
// 重试走「对账返回既有结果」而不是重跑。
type executionLedger struct {
	mu       sync.Mutex
	applied  map[string]int
	results  map[string]string
	loseNext bool
}

func newExecutionLedger() *executionLedger {
	return &executionLedger{applied: map[string]int{}, results: map[string]string{}}
}

func (e *executionLedger) Exec(executionID string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if res, ok := e.results[executionID]; ok {
		return res, nil // 对账命中：返回既有结果，不重跑
	}
	res := "executed:" + executionID
	e.results[executionID] = res
	e.applied[executionID]++
	if e.loseNext {
		e.loseNext = false
		return "", errInjectedResponseLost // 已施加，但调用方看到失败
	}
	return res, nil
}

func (e *executionLedger) totalApplied() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	total := 0
	for _, n := range e.applied {
		total += n
	}
	return total
}

// runK8sAPIResponseLossIdempotent 响应丢失后同执行标识重试：
//   - 首次调用返回「响应丢失」，但账本上已施加一次；
//   - 重试按同一标识对账，取回既有结果，施加次数仍为 1；
//   - 反向对照：换一个执行标识就是另一个动作，允许再施加一次（证明计数
//     不是永远为 1 的假判据）。
func runK8sAPIResponseLossIdempotent(t *testing.T) {
	ledger := newExecutionLedger()
	ledger.loseNext = true
	const execID = "run-42/task-1"

	if _, err := ledger.Exec(execID); !errors.Is(err, errInjectedResponseLost) {
		t.Fatalf("首次调用必须呈现响应丢失（实得 %v）", err)
	}
	if got := ledger.totalApplied(); got != 1 {
		t.Fatalf("响应丢失时作业已实际施加一次（实得 %d）——重试判据必须以这个事实为起点", got)
	}

	res, err := ledger.Exec(execID)
	if err != nil {
		t.Fatalf("同执行标识重试必须走对账而不是报错: %v", err)
	}
	if res != "executed:"+execID {
		t.Fatalf("重试必须返回既有执行的结果（实得 %q）", res)
	}
	if got := ledger.totalApplied(); got != 1 {
		t.Fatalf("同执行标识重试不得重复施加（实得 %d 次）——这正是「Job 已建、响应丢失」的双份作业缺陷", got)
	}

	if _, err := ledger.Exec("run-42/task-2"); err != nil {
		t.Fatal(err)
	}
	if got := ledger.totalApplied(); got != 2 {
		t.Fatalf("不同执行标识应各自施加一次（实得 %d）——反向对照失败说明判据把「只跑一次」写死了", got)
	}
}
