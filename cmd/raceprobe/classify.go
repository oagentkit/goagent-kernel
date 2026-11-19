// classify.go —— race 门禁失败形态的**判定**（纯函数层，不碰进程）。
//
// # 这层为什么必须与执行层分开
//
// 判据要能被**逐条钉死**：形态判定是纯函数（输入一段输出文本，输出一个结论），
// 于是「哪种输出算哪种形态」可以用构造出来的文本逐条钉住，不需要真的去跑一场
// 11 分钟的挂死。执行层只负责把真实输出**喂进来**。
//
// 混在一起时，判据只能等真挂死时才被验证——而真挂死在本机**不可稳定复现**
// （2026-11-18 实录：四轮全量重跑全绿，单包重跑 1.1~1.6 秒通过）。
// 判据跑不到 ⇒ 改坏了也没人知道 ⇒ 门禁是**看着在、其实没判**。
//
// # 六种形态与各自的处置
//
//	none          没失败。
//	data-race     检出数据竞争 ⇒ 代码面，按竞态报告里的栈修共享状态。
//	test-hang     用例挂死（Go 运行时活着，打出了 goroutine 栈）⇒ 代码面。
//	startup-wedge 启动期楔死（Go 运行时压根没起来，**没有 goroutine 栈**）⇒ 环境面。
//	test-failure  普通用例失败（断言失败）⇒ 代码面，按失败用例名修。
//	unknown       认不出 ⇒ **明说认不出**，不猜。
//
// # 判据是「栈在不在」而不是「超时了没有」
//
// 「超时」是**症状**，「有没有栈」是**机制指纹**：
//
//	· 用例挂死 ⇒ Go 运行时活到过 ⇒ 栈打得出来；
//	· 启动期楔死 ⇒ Go 运行时压根没起来 ⇒ 栈打不出来（连打栈的工具都不存在）。
//
// 能留下栈 ⇒ 运行时活到过 ⇒ 是用例问题。判「有没有超时」会把两种形态混成一种
// （都超时），处置就只剩猜——2026-11-18 那次 11m0s 的失败就是这么被归错的。
package main

import (
	"regexp"
	"sort"
	"strings"
)

// FailureKind 失败形态。
type FailureKind string

const (
	// KindNone 没有失败（门禁绿）。
	KindNone FailureKind = "none"
	// KindDataRace 检出数据竞争——代码面的真缺陷。
	KindDataRace FailureKind = "data-race"
	// KindTestHang 用例挂死：Go 运行时活着并打出 goroutine 栈 ⇒ 代码面。
	KindTestHang FailureKind = "test-hang"
	// KindStartupWedge 启动期楔死：Go 运行时压根没起来 ⇒ 环境面（不是代码缺陷）。
	KindStartupWedge FailureKind = "startup-wedge"
	// KindTestFailure 普通用例失败（断言失败）：不是竞态、也不是挂死 ⇒ 代码面。
	KindTestFailure FailureKind = "test-failure"
	// KindUnknown 认不出来的失败——明说认不出，不猜。
	KindUnknown FailureKind = "unknown"
)

// Verdict 一次失败的判定结论。
type Verdict struct {
	// Kind 失败形态。
	Kind FailureKind
	// Package 认定的受害包路径（空 = 认不出）。
	Package string
	// Timeout cmd/go 兜底闹钟上写的时限（如 "11m0s"）；空 = 没有这条记录。
	Timeout string
	// Detail 给人看的结论说明（含下一步该查什么）。
	Detail string
}

// 判定用到的输出标记。全部写成**常量声明**——判定词表的取值不许散落成无名字字面量。
const (
	// markerDataRace 竞态检测器报出数据竞争时的标记。
	markerDataRace = "WARNING: DATA RACE"
	// markerTestTimeout testing 包看门狗触发时的 panic 头。
	markerTestTimeout = "panic: test timed out after"
	// markerFailLine go test 的失败包行前缀。
	markerFailLine = "FAIL"
)

// stackMarkerRe Go 运行时打出的 goroutine 栈标记。
//
// 形态取自 testing 包的看门狗 panic 与 runtime 的栈帧头（"goroutine 1 [running]:"）。
var stackMarkerRe = regexp.MustCompile(`(?m)^goroutine [0-9]+ \[[^\]]*\]:`)

// cmdGoKillRe cmd/go 兜底闹钟杀掉测试二进制时打印的那行。
//
// 形态取自 cmd/go/internal/test/test.go："*** Test killed with quit: ran too long (11m0s)."
var cmdGoKillRe = regexp.MustCompile(`\*\*\* Test killed with (\S+): ran too long \(([^)]*)\)`)

// dataRaceRe 竞态检测器的报告标记。
var dataRaceRe = regexp.MustCompile(markerDataRace)

// hungTestRe testing 包看门狗触发时的 panic 头。
var hungTestRe = regexp.MustCompile(markerTestTimeout)

// Classify 从 race 门禁的输出文本判定失败形态。
//
// out 是 go test 的完整输出（stdout 与 stderr 合并——cmd/go 的兜底闹钟与
// "signal: quit" 打在 stderr 上，只看 stdout 会漏掉最关键的那条证据）。
//
// 判定顺序按**证据强度**排，不按出现顺序：
//  1. 数据竞争标记最硬（竞态检测器自己说的）⇒ 直接判 data-race；
//  2. testing 包的超时 panic ⇒ Go 运行时活着 ⇒ 用例挂死；
//  3. 有 goroutine 栈 ⇒ 运行时活到过 ⇒ 用例挂死；
//  4. 无栈但有 cmd/go 兜底闹钟记录 ⇒ 启动期楔死（环境面）；
//  5. 有失败包行但以上都不匹配 ⇒ 普通用例失败；
//  6. 什么都没有 ⇒ 认不出（明说认不出，不猜）。
//
// 为什么「认不出」不猜一个形态：处置方向完全不同（代码面 vs 机器面），
// 猜错比不猜更坏——不猜会让人去查日志，猜错会让人白查。
func Classify(out string) Verdict {
	if dataRaceRe.MatchString(out) {
		return Verdict{
			Kind:    KindDataRace,
			Package: firstPackage(FailedPackages(out)),
			Detail: "检出数据竞争：竞态检测器报了 DATA RACE —— 这是代码面的真缺陷，" +
				"按报告里的 goroutine 栈修共享状态（哪两个 goroutine 抢同一份内存）",
		}
	}
	if hungTestRe.MatchString(out) {
		return Verdict{
			Kind:    KindTestHang,
			Package: firstPackage(FailedPackages(out)),
			Detail: "用例挂死：Go 运行时活着并打出了 goroutine 栈 —— 这是代码面，" +
				"栈里指着卡住的 goroutine（栈顶那个就是凶手）",
		}
	}
	if stackMarkerRe.MatchString(out) {
		return Verdict{
			Kind:    KindTestHang,
			Package: firstPackage(FailedPackages(out)),
			Detail: "用例挂死：输出里有 goroutine 栈（Go 运行时活到过）—— 这是代码面，" +
				"按栈定位卡住的 goroutine",
		}
	}
	if m := cmdGoKillRe.FindStringSubmatch(out); m != nil {
		return Verdict{
			Kind:    KindStartupWedge,
			Package: firstPackage(FailedPackages(out)),
			Timeout: m[2],
			Detail: "启动期楔死：Go 的兜底闹钟到点杀掉了测试二进制，但**没有 goroutine 栈** —— " +
				"说明 Go 运行时压根没起来（进程卡在动态链接器启动期），不是用例问题。" +
				"查机器面：并行启动的测试二进制太多 / 内存压力 / 同时有别的构建在跑",
		}
	}
	if pkgs := FailedPackages(out); len(pkgs) > 0 {
		return Verdict{
			Kind:    KindTestFailure,
			Package: pkgs[0],
			Detail: "普通用例失败（不是竞态、也不是挂死）：失败包 " + pkgs[0] +
				" —— 按输出里的 `--- FAIL:` 用例名去修",
		}
	}
	if strings.Contains(out, markerFailLine) || strings.Contains(out, "panic:") {
		return Verdict{
			Kind: KindUnknown,
			Detail: "认不出的失败形态（有 FAIL/panic 但既无 goroutine 栈也无 cmd/go 兜底闹钟记录）—— " +
				"别猜：去看原始输出里第一个失败的包与它的报错",
		}
	}
	return Verdict{Kind: KindNone, Detail: "没有失败"}
}

// okRe go test 的「通过」包行（ok <pkg> <秒>）。
var okRe = regexp.MustCompile(`(?m)^ok\s+(\S+)`)

// failRe go test 的「失败」包行（FAIL <pkg> <秒>）。
var failRe = regexp.MustCompile(`(?m)^FAIL\s+(\S+)`)

// noTestRe go test 的「无测试文件」包行（? <pkg> [no test files]）。
var noTestRe = regexp.MustCompile(`(?m)^\?\s+(\S+)`)

// PassedPackages 取输出里**跑完且通过**的包。
func PassedPackages(out string) []string {
	return matchPackages(okRe, out)
}

// FailedPackages 取输出里**跑完但失败**的包。
//
// 注意 cmd/go 的收尾会打一行**不带包名**的 "FAIL"（整轮失败的汇总）——
// 正则要求后面有非空白内容，故那行不会被当成包。
func FailedPackages(out string) []string {
	return matchPackages(failRe, out)
}

// NoTestPackages 取输出里「无测试文件」的包（它们不需要跑，也不算失败）。
func NoTestPackages(out string) []string {
	return matchPackages(noTestRe, out)
}

// firstPackage 取包列表里的第一个（空列表给空串）。
func firstPackage(pkgs []string) string {
	if len(pkgs) == 0 {
		return ""
	}
	return pkgs[0]
}

// matchPackages 取正则捕获到的包名（去重 + 排序，保证输出稳定）。
func matchPackages(re *regexp.Regexp, out string) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		if len(m) < 2 || m[1] == "" {
			continue
		}
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		pkgs = append(pkgs, m[1])
	}
	sort.Strings(pkgs)
	return pkgs
}

// RecheckTargets 取**串行复核**该重跑的包集合。
//
// 取的是「跑过但没通过」的包（FAIL 包行）：它们是本轮唯一**真的跑起来过**、
// 且**没拿到通过结论**的包。启动期楔死时，被卡住的那个包正是以 FAIL 包行
// 出现在输出里的（cmd/go 收尾时替它记了这一笔）。
//
// 为什么不重跑「压根没排上」的包：它们**从没运行过**，没有任何证据指向它们，
// 重跑它们只是把复核变成一场全量重跑（分钟级），换不来任何新信息。
func RecheckTargets(out string) []string {
	return FailedPackages(out)
}
