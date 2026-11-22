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

// TestWriteEvidence 钉住取证文件落盘。
func TestWriteEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := writeEvidence(dir, "20260101T000000Z", "hello"); err != nil {
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
