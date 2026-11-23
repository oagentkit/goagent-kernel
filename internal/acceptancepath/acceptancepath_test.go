package acceptancepath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testNow 固定时钟：用例不依赖「今天是哪天」——否则同一份代码今天绿、明天红，
// 而红的原因与被判事实无关。
var testNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// goodMakefile 一份带验收路径标记的 Makefile。
const goodMakefile = "bench: ## 性能基线复跑  #acceptance-path: bench-baseline\n\t@echo bench\n"

// fixture 搭一个临时仓：Makefile + 若干文件。
type fixture struct {
	dir string
}

func newFixture(t *testing.T, makefile string, files map[string]string) *fixture {
	t.Helper()
	dir := t.TempDir()
	if makefile != "" {
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o600); err != nil {
			t.Fatalf("写 Makefile 失败：%v", err)
		}
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("写 %s 失败：%v", name, err)
		}
	}
	return &fixture{dir: dir}
}

// judge 跑一次判据（根目录与时钟都注入 ⇒ 确定性）。
func (f *fixture) judge(reg *Registry) Result {
	return Judge(reg, Options{Root: f.dir, Now: testNow})
}

// evidenceBody 造一份合规的证据文件。
func evidenceBody(id, ranAt, conclusion string) string {
	return strings.Join([]string{
		"# 机器可读登记块",
		FieldID + " " + id,
		FieldRanAt + " " + ranAt,
		FieldConclusion + " " + conclusion,
	}, "\n")
}

// goodRegistry 一份合规的登记面（本仓自有路径 + 证据齐全）。
func goodRegistry() *Registry {
	return &Registry{
		Updated: "2026-11-23",
		Paths: []Path{{
			ID:         "bench-baseline",
			Scope:      "真机性能基线复跑",
			Command:    "make bench",
			Evidence:   "docs/bench.md",
			RanAt:      "2026-11-13",
			Conclusion: "pass",
		}},
	}
}

func problemsText(r Result) string {
	return strings.Join(r.Problems, "\n")
}

// 判据 1/2/3 的正面路径：登记齐、证据齐、入口齐 ⇒ 无问题。
func TestJudge_合规登记全绿(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) != 0 {
		t.Fatalf("合规登记不该有违反，实际：%s", problemsText(res))
	}
	if len(res.Ran) != 1 || res.Ran[0] != "bench-baseline" {
		t.Fatalf("跑过的路径应是 bench-baseline，实际：%v", res.Ran)
	}
	if len(res.Never) != 0 {
		t.Fatalf("没有「从未跑过」的路径，实际：%v", res.Never)
	}
}

// 判据 1：登记了却没证据 ⇒ 红，且**点名**（Never 里有它）。
func TestJudge_证据文件不存在即红并点名(t *testing.T) {
	f := newFixture(t, goodMakefile, nil)
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("证据文件不存在时必须判红（登记了却从没跑过 ⇒ 前提不成立）")
	}
	if !strings.Contains(problemsText(res), "从未被执行过") {
		t.Fatalf("问题说明应点明「从未被执行过」，实际：%s", problemsText(res))
	}
	if len(res.Never) != 1 || res.Never[0] != "bench-baseline" {
		t.Fatalf("未跑过的路径必须显形点名，实际：%v", res.Never)
	}
}

// 判据 1 的另一形态：连证据文件路径都没登记 ⇒ 同样红（不能「没登记 = 不用查」）。
func TestJudge_没有登记证据即红(t *testing.T) {
	f := newFixture(t, goodMakefile, nil)
	reg := goodRegistry()
	reg.Paths[0].Evidence = ""
	res := f.judge(reg)
	if len(res.Problems) == 0 {
		t.Fatal("没有登记证据时必须判红")
	}
	if len(res.Never) != 1 {
		t.Fatalf("未跑过的路径必须显形点名，实际：%v", res.Never)
	}
}

// 判据 5：证据里没有机器可读字段行 ⇒ 红（正文里提到这条路径不算证据）。
func TestJudge_证据没有字段行即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": "本文提到 bench-baseline 这条路径做过复跑。",
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("证据没有机器可读字段行时必须判红")
	}
	if !strings.Contains(problemsText(res), FieldID) {
		t.Fatalf("问题说明应点明缺哪个字段，实际：%s", problemsText(res))
	}
}

// 判据 5：证据里的 id 与登记不符 ⇒ 红（复制别的路径的结论）。
func TestJudge_证据id与登记不符即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("other-path", "2026-11-13", "pass"),
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("证据 id 与登记不符时必须判红")
	}
	if len(res.Never) != 0 {
		t.Fatalf("这条是「对不上」而不是「没跑过」，不该进 Never，实际：%v", res.Never)
	}
}

// 判据 5：证据里的 ran_at 与登记不符 ⇒ 红。
func TestJudge_证据时间与登记不符即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-01-01", "pass"),
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("证据时间与登记不符时必须判红")
	}
}

// 判据 5：证据里的结论与登记不符 ⇒ 红。
func TestJudge_证据结论与登记不符即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "fail"),
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("证据结论与登记不符时必须判红")
	}
}

// ran_at 不是日期 ⇒ 红（时间戳是证据的一部分）。
func TestJudge_ranAt非法即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	reg := goodRegistry()
	reg.Paths[0].RanAt = "去年"
	res := f.judge(reg)
	if len(res.Problems) == 0 {
		t.Fatal("ran_at 非法时必须判红")
	}
}

// 判据 3：入口目标不存在 ⇒ 红（判据体搬走了、入口没跟着搬）。
func TestJudge_入口目标不存在即红(t *testing.T) {
	f := newFixture(t, "other: ## 别的目标\n\t@echo other\n", map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("入口目标不存在时必须判红")
	}
	if !strings.Contains(problemsText(res), "在 Makefile 里不存在") {
		t.Fatalf("问题说明应点明缺哪个目标，实际：%s", problemsText(res))
	}
}

// 判据 3：目标存在但没有验收路径标记 ⇒ 红（双向对账缺一半）。
func TestJudge_入口目标没有标记即红(t *testing.T) {
	f := newFixture(t, "bench: ## 性能基线复跑\n\t@echo bench\n", map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("入口目标没打验收路径标记时必须判红")
	}
	if !strings.Contains(problemsText(res), "acceptance-path") {
		t.Fatalf("问题说明应点明缺标记，实际：%s", problemsText(res))
	}
}

// 判据 3：目标上标的 id 与登记的 id 不是同一条 ⇒ 红。
func TestJudge_入口标记的id与登记不符即红(t *testing.T) {
	f := newFixture(t, "bench: ## 基线  # acceptance-path: other-id\n\t@echo bench\n", map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	res := f.judge(goodRegistry())
	if len(res.Problems) == 0 {
		t.Fatal("入口标的 id 与登记不符时必须判红")
	}
}

// 判据 4：Makefile 声明过的验收路径没进登记面 ⇒ 红（漏登记）。
func TestJudge_Makefile声明的路径未登记即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	reg := goodRegistry()
	reg.Paths = nil
	res := f.judge(reg)
	if len(res.Problems) == 0 {
		t.Fatal("Makefile 声明过的验收路径未登记时必须判红")
	}
	if !strings.Contains(problemsText(res), "没进登记面") {
		t.Fatalf("问题说明应点明漏登记，实际：%s", problemsText(res))
	}
}

// 同一个 id 登记两条 ⇒ 红（证据与结论对不上时无从定位）。
func TestJudge_重复id即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	reg := goodRegistry()
	reg.Paths = append(reg.Paths, reg.Paths[0])
	res := f.judge(reg)
	if len(res.Problems) == 0 {
		t.Fatal("重复 id 必须判红")
	}
}

// 没有 id 的登记 ⇒ 红（无法定位，也无法点名）。
func TestJudge_没有id即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	reg := goodRegistry()
	reg.Paths[0].ID = ""
	res := f.judge(reg)
	if len(res.Problems) == 0 {
		t.Fatal("没有 id 的登记必须判红")
	}
}

// 登记面的 updated 不是日期 ⇒ 红。
func TestJudge_Updated非法即红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	reg := goodRegistry()
	reg.Updated = "2026/11/23"
	res := f.judge(reg)
	if len(res.Problems) == 0 {
		t.Fatal("updated 非法时必须判红")
	}
}

// 判据 2：过期**只回显不判红**。
func TestJudge_过期只回显不判红(t *testing.T) {
	f := newFixture(t, goodMakefile, map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2020-01-01", "pass"),
	})
	reg := goodRegistry()
	reg.Paths[0].RanAt = "2020-01-01"
	res := f.judge(reg)
	if len(res.Problems) != 0 {
		t.Fatalf("过期只回显不该判红，实际：%s", problemsText(res))
	}
	if len(res.Echoes) == 0 {
		t.Fatal("过期必须回显")
	}
}

// 跨仓路径：入口存在性本仓无从核实 ⇒ 只回显，不判红。
func TestJudge_跨仓路径不核验入口(t *testing.T) {
	f := newFixture(t, "", map[string]string{
		"docs/k8s.md": evidenceBody("k8s-egress-denied", "2026-11-19", "pass"),
	})
	reg := &Registry{
		Updated: "2026-11-23",
		Paths: []Path{{
			ID:         "k8s-egress-denied",
			Command:    "make k8s-e2e",
			Owner:      "goagent-runtime",
			Evidence:   "docs/k8s.md",
			RanAt:      "2026-11-19",
			Conclusion: "pass",
		}},
	}
	res := f.judge(reg)
	if len(res.Problems) != 0 {
		t.Fatalf("跨仓路径不核验入口，不该判红，实际：%s", problemsText(res))
	}
	if len(res.Echoes) == 0 {
		t.Fatal("跨仓路径应回显「入口存在性本仓无从核实」")
	}
}

// 跨仓路径的证据在**本仓**：证据不在本仓 ⇒ 本仓核验不到 ⇒ 判红（不许靠人记）。
func TestJudge_跨仓路径的证据缺失仍即红(t *testing.T) {
	f := newFixture(t, "", nil)
	reg := &Registry{
		Updated: "2026-11-23",
		Paths: []Path{{
			ID:         "k8s-egress-denied",
			Command:    "make k8s-e2e",
			Owner:      "goagent-runtime",
			Evidence:   "docs/k8s.md",
			RanAt:      "2026-11-19",
			Conclusion: "pass",
		}},
	}
	res := f.judge(reg)
	if len(res.Problems) == 0 {
		t.Fatal("证据不在本仓时必须判红（核验不到 ≠ 没违反）")
	}
}

// Makefile 读不到 ⇒ **给不出结论**（Fatal），不能当成「没有违反」。
func TestJudge_Makefile读不到即Fatal(t *testing.T) {
	f := newFixture(t, "", map[string]string{
		"docs/bench.md": evidenceBody("bench-baseline", "2026-11-13", "pass"),
	})
	res := f.judge(goodRegistry())
	if len(res.Fatal) == 0 {
		t.Fatal("Makefile 读不到时必须给不出结论（Fatal）")
	}
}

// markInLine：双井号说明里的字样不算「目标声明自己是验收入口」。
func TestMarkInLine_双井号不算标记(t *testing.T) {
	if _, ok := markInLine("bench: ## 说明里写了 # acceptance-path: bench-baseline"); ok {
		t.Fatal("说明文字里的引用形态（带空格）不该被当成目标上的声明")
	}
	if id, ok := markInLine("bench: ## 说明  #acceptance-path: bench-baseline"); !ok || id != "bench-baseline" {
		t.Fatalf("目标行上的标记应被认出，实际：%q ok=%v", id, ok)
	}
}

// makeTarget：跳过选项参数，取第一个非选项参数。
func TestMakeTarget(t *testing.T) {
	if target, ok := makeTarget("make -C dir bench"); !ok || target != "bench" {
		t.Fatalf("应取到 bench，实际：%q ok=%v", target, ok)
	}
	if _, ok := makeTarget("go test ./..."); ok {
		t.Fatal("非 make 形态不应被当成 make 目标")
	}
}
