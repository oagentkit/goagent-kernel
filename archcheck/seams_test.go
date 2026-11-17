// seams_test.go —— 接缝门禁自身的用例（issue #369/#370）。
//
// 三种形态各自的判据都要有正反两条：命中必须报出来（否则门禁变绿而没人知道），
// 合法形态不得误伤（否则维护者学会忽略它）。另加一条覆盖真实仓的清单用例：
// 导出时钟接缝声明点必须全部带齐标记且枚举数不为零（形态 B 的清单完备性）。
package archcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSeamFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func seamFixtureRules() SeamRules {
	return SeamRules{
		Enabled:         boolPtr(true),
		Severity:        "error",
		MinDeclarations: 1,
		LockScan:        &SeamLockScan{Enabled: boolPtr(true), Severity: "warn"},
		PanicScan:       &SeamPanicScan{Enabled: boolPtr(true), GuardedCalls: []string{"RenewBatch"}},
	}
}

const seamPkg = `package sample

import "time"

// WithClock 注入时钟。
//
// # 接缝契约（issue #369）
// @seam: no-reentry no-blocking panic-caller
func WithClock(clock func() time.Time) { _ = clock }
`

// TestSeamContractComplete 三条标记齐全 ⇒ 零违规。
func TestSeamContractComplete(t *testing.T) {
	root := t.TempDir()
	writeSeamFixture(t, root, map[string]string{"sample/clock.go": seamPkg})
	vs, err := seamContractViolations(root, seamFixtureRules(), "architecture-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("标记齐全不得报违规:\n%s", textsOf(vs))
	}
}

// TestSeamContractMissingMarker 删掉整行标记 ⇒ 必须红（反向验证）。
func TestSeamContractMissingMarker(t *testing.T) {
	root := t.TempDir()
	src := strings.Replace(seamPkg, "@seam: no-reentry no-blocking panic-caller", "", 1)
	writeSeamFixture(t, root, map[string]string{"sample/clock.go": src})
	vs, err := seamContractViolations(root, seamFixtureRules(), "architecture-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(vs, "no-reentry") || !containsText(vs, "panic 处置") {
		t.Fatalf("缺标记必须报出缺失项，实得:\n%s", textsOf(vs))
	}
}

// TestSeamContractMissingOneToken 只删一条约束 ⇒ 也必须红。
func TestSeamContractMissingOneToken(t *testing.T) {
	root := t.TempDir()
	src := strings.Replace(seamPkg, "no-blocking ", "", 1)
	writeSeamFixture(t, root, map[string]string{"sample/clock.go": src})
	vs, err := seamContractViolations(root, seamFixtureRules(), "architecture-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(vs, "no-blocking") {
		t.Fatalf("缺 no-blocking 必须报出，实得:\n%s", textsOf(vs))
	}
}

// TestSeamContractAmbiguousPanic 两个 panic 处置都写 ⇒ 判违规（不许含糊）。
func TestSeamContractAmbiguousPanic(t *testing.T) {
	root := t.TempDir()
	src := strings.Replace(seamPkg, "panic-caller", "panic-caller panic-isolated", 1)
	writeSeamFixture(t, root, map[string]string{"sample/clock.go": src})
	vs, err := seamContractViolations(root, seamFixtureRules(), "architecture-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(vs, "panic 处置") {
		t.Fatalf("panic 处置必须恰一个，实得:\n%s", textsOf(vs))
	}
}

// TestSeamContractInterfaceAndField 接口方法与导出结构体字段同样在判定面内。
func TestSeamContractInterfaceAndField(t *testing.T) {
	root := t.TempDir()
	src := `package sample

import "time"

type Clocked interface {
	UseClock(now func() time.Time)
}

type Config struct {
	Now func() time.Time
}
`
	writeSeamFixture(t, root, map[string]string{"sample/clock.go": src})
	vs, err := seamContractViolations(root, seamFixtureRules(), "architecture-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(vs, "Clocked.UseClock") || !containsText(vs, "Config.Now") {
		t.Fatalf("接口方法与结构体字段都必须被枚举，实得:\n%s", textsOf(vs))
	}
}

// TestSeamContractEmptySurface 枚举到 0 个声明 ⇒ 门禁必须响亮失败。
func TestSeamContractEmptySurface(t *testing.T) {
	root := t.TempDir()
	writeSeamFixture(t, root, map[string]string{"sample/plain.go": "package sample\n"})
	vs, err := seamContractViolations(root, seamFixtureRules(), "architecture-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(vs, "判定面失效") {
		t.Fatalf("0 个接缝必须报判定面失效，实得:\n%s", textsOf(vs))
	}
}

// TestSeamLockScanCatchesAccessorInLock 锁内调时钟访问器必须报出（形态 A）。
func TestSeamLockScanCatchesAccessorInLock(t *testing.T) {
	root := t.TempDir()
	src := `package sample

import (
	"sync"
	"time"
)

type store struct {
	mu    sync.Mutex
	clock func() time.Time
}

func (s *store) bad() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock()
}
`
	writeSeamFixture(t, root, map[string]string{"sample/store.go": src})
	vs, err := seamLockHeldViolations(root, seamFixtureRules().lockScan())
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(vs, "疑似在锁内调用时钟访问器 clock") {
		t.Fatalf("锁内调 clock 必须报出，实得:\n%s", textsOf(vs))
	}
}

// TestSeamLockScanAllowsOutsideLock 锁外调访问器不得误报。
func TestSeamLockScanAllowsOutsideLock(t *testing.T) {
	root := t.TempDir()
	src := `package sample

import (
	"sync"
	"time"
)

type store struct {
	mu    sync.Mutex
	clock func() time.Time
}

func (s *store) good() time.Time {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return now
}
`
	writeSeamFixture(t, root, map[string]string{"sample/store.go": src})
	vs, err := seamLockHeldViolations(root, seamFixtureRules().lockScan())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("锁外调访问器不得误报:\n%s", textsOf(vs))
	}
}

// TestSeamPanicScanCatchEscaping 直接调登记接缝（无隔离）必须报出（形态 C）。
func TestSeamPanicScanCatchEscaping(t *testing.T) {
	root := t.TempDir()
	src := `package sample

type board struct{}

func (b *board) RenewBatch() {}

func run(b *board) { b.RenewBatch() }
`
	writeSeamFixture(t, root, map[string]string{"sample/r.go": src})
	vs, err := seamPanicViolations(root, seamFixtureRules().panicScan())
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(vs, "未做 panic 隔离") {
		t.Fatalf("无隔离的登记调用必须报出，实得:\n%s", textsOf(vs))
	}
}

// TestSeamPanicScanAllowsWrapper 隔离包装器内的调用不得误报。
func TestSeamPanicScanAllowsWrapper(t *testing.T) {
	root := t.TempDir()
	src := `package sample

import "example.test/async"

type board struct{}

func (b *board) RenewBatch() {}

func run(b *board) {
	async.GoSafe(func() { b.RenewBatch() })
}
`
	writeSeamFixture(t, root, map[string]string{"sample/r.go": src})
	vs, err := seamPanicViolations(root, seamFixtureRules().panicScan())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("GoSafe 内不得误报:\n%s", textsOf(vs))
	}
}

// TestSeamPanicScanAllowsRecoverGuard 函数自带 defer recover 不得误报。
func TestSeamPanicScanAllowsRecoverGuard(t *testing.T) {
	root := t.TempDir()
	src := `package sample

type board struct{}

func (b *board) RenewBatch() {}

func run(b *board) {
	defer func() {
		if r := recover(); r != nil {
			_ = r
		}
	}()
	b.RenewBatch()
}
`
	writeSeamFixture(t, root, map[string]string{"sample/r.go": src})
	vs, err := seamPanicViolations(root, seamFixtureRules().panicScan())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("defer recover 兜底内不得误报:\n%s", textsOf(vs))
	}
}

// TestSeamContractBlocksRun 缺标记必须让整次门禁阻断（HasBlocking = 退出码 1）。
//
// 单测 seamContractViolations 只证明「报了出来」；这里连同 severity 分流与
// HasBlocking 一起钉住——否则一条 warn 级/未登记级别的规则可以报而不阻断。
func TestSeamContractBlocksRun(t *testing.T) {
	root := t.TempDir()
	src := strings.Replace(seamPkg, "@seam: no-reentry no-blocking panic-caller", "", 1)
	writeSeamFixture(t, root, map[string]string{"sample/clock.go": src})
	pol := &Policy{
		Version: 1,
		Seams: &SeamRules{
			Enabled:         boolPtr(true),
			Severity:        "error",
			MinDeclarations: 1,
		},
	}
	res, err := RunWithOptions(root, pol, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasBlocking() {
		t.Fatalf("缺标记必须是阻断级（退出码 1），实得 Blocking=%d Warnings=%d\n%s",
			res.Blocking, res.Warnings, textsOf(res.Violations))
	}
}

// TestRepoClockSeamsAnnotated 真实仓的导出时钟接缝必须全部带齐标记。
//
// min_declarations 取 13（当前枚举面：coord×2 / controlplane / handoff×3 /
// reconcile / scheduling / sidetest / boardtest / ticket×2 / worker）。
// 少一个声明（改签名/删接缝）时本用例会提醒同步清单；多出的声明缺标记则红。
func TestRepoClockSeamsAnnotated(t *testing.T) {
	rules := seamFixtureRules()
	rules.MinDeclarations = 13
	vs, err := seamContractViolations("..", rules, "architecture-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("真实仓接缝标记不完整:\n%s", textsOf(vs))
	}
}
