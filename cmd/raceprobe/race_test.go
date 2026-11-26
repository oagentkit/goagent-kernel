// race_test.go —— 编排层的行为钉住（用假 spawner，不真跑 race）。
//
// # 这层最要紧的三条
//
//  1. **形态是启动期楔死时，必须做串行复核**（反事实实验）——不复核就判绿
//     是把「断言」当「证据」。
//  2. **复核通过才判绿**；复核失败/没复核 ⇒ 判红。
//  3. **看门狗必须晚于 cmd/go 的兜底闹钟**（timeout + 1m）——早了就拿不到
//     启动期楔死的指纹，判定会退化成 unknown。
package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/raceprobe"
)

// fakeResult 一次假执行的预设结果。
type fakeResult struct {
	code   int
	stdout string
	stderr string
	err    error
}

// fakeSpawner 返回按调用次序取预设结果的假 spawner，以及调用计数。
func fakeSpawner(results ...fakeResult) (spawner, *int) {
	calls := 0
	sp := func(_ context.Context, _ []string, _ []string, out, errOut io.Writer) (int, error) {
		idx := calls
		calls++
		if idx >= len(results) {
			idx = len(results) - 1
		}
		r := results[idx]
		if r.stdout != "" {
			_, _ = io.WriteString(out, r.stdout)
		}
		if r.stderr != "" {
			_, _ = io.WriteString(errOut, r.stderr)
		}
		return r.code, r.err
	}
	return sp, &calls
}

// testOptions 造一份可跑的编排参数（输出接到丢弃口，取证落临时目录）。
func testOptions(t *testing.T, sp spawner) options {
	t.Helper()
	return options{
		packages:       "./...",
		goTimeout:      10 * time.Minute,
		watchdog:       15 * time.Minute,
		outDir:         t.TempDir(),
		recheck:        true,
		recheckTimeout: 10 * time.Minute,
		spawn:          sp,
		now:            time.Now,
		stdout:         io.Discard,
		stderr:         io.Discard,
	}
}

// TestRunRaceGreen 钉住绿路径：只跑一轮，不做复核。
func TestRunRaceGreen(t *testing.T) {
	sp, calls := fakeSpawner(fakeResult{code: 0, stdout: "ok  \tpkg\t1.344s\n"})
	o := testOptions(t, sp)
	code, msg := runRace(context.Background(), o)
	if code != 0 {
		t.Fatalf("绿路径应判绿，得到 code=%d msg=%s", code, msg)
	}
	if *calls != 1 {
		t.Fatalf("绿路径只该跑一轮，实际 %d 轮", *calls)
	}
}

// TestRunRaceDataRaceIsRed 钉住数据竞争判红且**不**做复核（它是代码面）。
func TestRunRaceDataRaceIsRed(t *testing.T) {
	sp, calls := fakeSpawner(fakeResult{code: 1, stdout: raceOut})
	o := testOptions(t, sp)
	code, msg := runRace(context.Background(), o)
	if code != 1 {
		t.Fatalf("数据竞争必须判红，得到 code=%d", code)
	}
	if !strings.Contains(msg, string(raceprobe.KindDataRace)) {
		t.Fatalf("结论里没有形态：%s", msg)
	}
	if *calls != 1 {
		t.Fatalf("数据竞争不该触发复核，实际 %d 轮", *calls)
	}
}

// TestRunRaceStartupWedgeRecheckRecovers 钉住「楔死 + 复核通过 ⇒ 判绿但留痕」。
func TestRunRaceStartupWedgeRecheckRecovers(t *testing.T) {
	sp, calls := fakeSpawner(
		fakeResult{code: 1, stderr: wedgeOut},
		fakeResult{code: 0, stdout: "ok  \tgithub.com/yi-nology/goagent-kernel/statemachine\t1.2s\n"},
	)
	o := testOptions(t, sp)
	code, msg := runRace(context.Background(), o)
	if code != 0 {
		t.Fatalf("环境面已复核应判绿，得到 code=%d msg=%s", code, msg)
	}
	if *calls != 2 {
		t.Fatalf("必须做一次串行复核（两轮），实际 %d 轮", *calls)
	}
	if !strings.Contains(msg, "串行复核") {
		t.Fatalf("结论里没有复核说明：%s", msg)
	}
	// 留痕：原始输出 + 复核输出 + 结论块，三份都得在。
	var logs, verdicts int
	entries, err := os.ReadDir(o.outDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".log"):
			logs++
		case strings.HasSuffix(e.Name(), ".verdict.txt"):
			verdicts++
		}
	}
	if logs != 2 {
		t.Fatalf("原始输出与复核输出两份日志都得落盘，实际 %d 份", logs)
	}
	if verdicts != 1 {
		t.Fatalf("结论块必须落盘，实际 %d 份", verdicts)
	}
}

// TestRunRaceStartupWedgeWithoutRecheckIsRed 是**反向验证**：
// 关掉复核（或没跑复核）时，楔死必须判红——
// 一个「楔死直接判绿」的实现会在这条上变红。
func TestRunRaceStartupWedgeWithoutRecheckIsRed(t *testing.T) {
	sp, calls := fakeSpawner(fakeResult{code: 1, stderr: wedgeOut})
	o := testOptions(t, sp)
	o.recheck = false
	code, msg := runRace(context.Background(), o)
	if code != 1 {
		t.Fatalf("没复核不得判绿，得到 code=%d msg=%s", code, msg)
	}
	if *calls != 1 {
		t.Fatalf("没复核时不得发起复核，实际 %d 轮", *calls)
	}
}

// TestRunRaceStartupWedgeRecheckFailsStaysRed 是**反向验证**：
// 复核失败时必须判红，且结论按**复核**的形态（不是首轮的楔死）。
func TestRunRaceStartupWedgeRecheckFailsStaysRed(t *testing.T) {
	sp, calls := fakeSpawner(
		fakeResult{code: 1, stderr: wedgeOut},
		fakeResult{code: 1, stdout: failOut},
	)
	o := testOptions(t, sp)
	code, msg := runRace(context.Background(), o)
	if code != 1 {
		t.Fatalf("复核失败必须判红，得到 code=%d", code)
	}
	if *calls != 2 {
		t.Fatalf("必须做一次串行复核，实际 %d 轮", *calls)
	}
	if !strings.Contains(msg, string(raceprobe.KindTestFailure)) {
		t.Fatalf("复核失败时应按复核结论判形态：%s", msg)
	}
}

// TestRunRaceTestFailureIsRed 钉住普通用例失败判红（不做复核）。
func TestRunRaceTestFailureIsRed(t *testing.T) {
	sp, calls := fakeSpawner(fakeResult{code: 1, stdout: failOut})
	o := testOptions(t, sp)
	code, msg := runRace(context.Background(), o)
	if code != 1 {
		t.Fatalf("普通用例失败必须判红，得到 code=%d msg=%s", code, msg)
	}
	if *calls != 1 {
		t.Fatalf("普通用例失败不该触发复核，实际 %d 轮", *calls)
	}
}

// TestRunRaceBadBudgetIsConfigError 钉住预算不合法时判「配置错」（退出码 2）。
func TestRunRaceBadBudgetIsConfigError(t *testing.T) {
	sp, _ := fakeSpawner(fakeResult{code: 0})
	o := testOptions(t, sp)
	o.watchdog = 10 * time.Minute
	code, msg := runRace(context.Background(), o)
	if code != 2 {
		t.Fatalf("预算不合法应判配置错（2），得到 code=%d msg=%s", code, msg)
	}
}

// TestValidateBudget 钉住「看门狗必须晚于 cmd/go 兜底闹钟」。
//
// 反向验证：把 +1m 那条规则去掉（看门狗只要晚于用例超时即可）的实现，
// 会在第一条用例上变红——而那条规则正是楔死指纹的来源。
func TestValidateBudget(t *testing.T) {
	if err := validateBudget(10*time.Minute, 11*time.Minute); err == nil {
		t.Fatal("看门狗等于 cmd/go 兜底闹钟时必须报错（否则拿不到楔死指纹）")
	}
	if err := validateBudget(10*time.Minute, 10*time.Minute); err == nil {
		t.Fatal("看门狗早于用例超时时必须报错")
	}
	if err := validateBudget(10*time.Minute, 15*time.Minute); err != nil {
		t.Fatalf("合法预算不该报错：%v", err)
	}
	if err := validateBudget(0, 15*time.Minute); err == nil {
		t.Fatal("用例超时必须为正")
	}
	if err := validateBudget(10*time.Minute, 0); err == nil {
		t.Fatal("看门狗必须为正")
	}
}

// TestTestArgs 钉住 go test 参数的拼装（包列表必须在最后）。
func TestTestArgs(t *testing.T) {
	o := options{goTimeout: 10 * time.Minute}
	args := o.testArgs("1", []string{"pkg/a", "pkg/b"})
	want := []string{"test", "-p", "1", "-race", "-count=1", "-timeout", "10m0s", "pkg/a", "pkg/b"}
	if len(args) != len(want) {
		t.Fatalf("参数个数错：%v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("参数错：%v，期望 %v", args, want)
		}
	}
	plain := o.testArgs("", []string{"./..."})
	if len(plain) != 6 || plain[0] != "test" || plain[len(plain)-1] != "./..." {
		t.Fatalf("不带 -p 时参数错：%v", plain)
	}
}

// TestTestArgsReuseShapes 钉住**复用形态**的参数拼装（§26 建议 2：fault-injection
// 走同一套编排时，go test 的参数必须与它原来的形态逐字一致）。
//
// 反向验证：把 -race 无条件加回去 / 丢掉 -run / 丢掉 -v，本表任一行即红——
// 「复用」若偷偷改了被复用门禁的参数，它就不再是同一个门禁了。
func TestTestArgsReuseShapes(t *testing.T) {
	// fault-injection 形态：不判竞态、钉 ^TestFault、要 -v。
	o := options{goTimeout: 7 * time.Minute, noRace: true, runPattern: "^TestFault", verbose: true}
	args := o.testArgs("", []string{"./..."})
	want := []string{"test", "-v", "-count=1", "-timeout", "7m0s", "-run", "^TestFault", "./..."}
	if len(args) != len(want) {
		t.Fatalf("参数个数错：%v，期望 %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("参数错：%v，期望 %v", args, want)
		}
	}
	for _, a := range args {
		if a == "-race" {
			t.Fatalf("noRace 形态不该带 -race：%v", args)
		}
	}
	// race 形态（缺省）：带 -race、不带 -run / -v。
	r := options{goTimeout: 10 * time.Minute}
	ra := r.testArgs("", []string{"./..."})
	hasRace, hasRun, hasV := false, false, false
	for _, a := range ra {
		switch a {
		case "-race":
			hasRace = true
		case "-run":
			hasRun = true
		case "-v":
			hasV = true
		}
	}
	if !hasRace || hasRun || hasV {
		t.Fatalf("race 缺省形态错：%v", ra)
	}
}

// TestChildEnvCgoFollowsRaceSwitch 钉住 cgo 注入跟着 -race 开关走。
//
// 反向验证：把 childEnv 改回无条件注入 1 ⇒ 关 race 的复用形态会绑上 cgo
// （fault-injection 白慢一轮，且本机无编译器时直接红）；改成不注入 ⇒ race 形态
// 在 Linux 上 `-race requires cgo` 报错。
func TestChildEnvCgoFollowsRaceSwitch(t *testing.T) {
	t.Setenv("CGO_ENABLED", "1") // 父进程取值相反也必须被字面注入覆盖
	cgoOf := func(enableRace bool) string {
		for _, kv := range childEnv(enableRace) {
			if strings.HasPrefix(kv, "CGO_ENABLED=") {
				return kv
			}
		}
		return ""
	}
	if got := cgoOf(true); got != "CGO_ENABLED=1" {
		t.Fatalf("race 形态的 cgo 注入错：%q", got)
	}
	if got := cgoOf(false); got != "CGO_ENABLED=0" {
		t.Fatalf("关 race 形态的 cgo 注入错：%q", got)
	}
}

// TestEvidenceFileNameFollowsLabel 钉住 label 进取证文件名——
// 两个门禁共用一个取证目录时，文件名不带门禁名就会互相覆盖。
func TestEvidenceFileNameFollowsLabel(t *testing.T) {
	dir := t.TempDir()
	if err := writeEvidence(dir, "fault-injection", "20260101T000000Z", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fault-injection-20260101T000000Z.verdict.txt")); err != nil {
		t.Fatalf("取证文件没按 label 命名：%v", err)
	}
}

// TestWriteEvidence 钉住取证文件落盘。
func TestWriteEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := writeEvidence(dir, "race", "20260101T000000Z", "hello"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "race-20260101T000000Z.verdict.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("取证内容错：%q", string(b))
	}
}
