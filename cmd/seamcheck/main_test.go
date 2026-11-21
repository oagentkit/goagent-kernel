package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestConstraints(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want []string
	}{
		{"三条写齐", "不得重入；不得阻塞；panic 隔离。", []string{constraintReentry, constraintBlocking, constraintPanic}},
		{"同义写法", "不可重入 / 禁止阻塞 / panic 由内核隔离", []string{constraintReentry, constraintBlocking, constraintPanic}},
		{"只有一条", "实现不得重入。", []string{constraintReentry}},
		{"描述实现不算约束", "通知在锁外、不阻塞发布面。", nil},
		{"空文档", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := constraints(tc.doc)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("constraints(%q) = %v, want %v", tc.doc, got, tc.want)
			}
		})
	}
}

func seam(id string, written ...string) Seam {
	return Seam{ID: id, Kind: "interface", Callsites: 1, Written: written}
}

func TestCompare(t *testing.T) {
	base := Report{Declarations: 2, Callsites: 2, FullyWritten: 1, Seams: []Seam{
		seam("a.Iface", constraintReentry, constraintBlocking, constraintPanic),
		seam("b.Func"),
	}}

	t.Run("相同即绿", func(t *testing.T) {
		if err := compare(base, base); err != nil {
			t.Fatalf("相同基线不应报错: %v", err)
		}
	})
	t.Run("接缝消失", func(t *testing.T) {
		cur := base
		cur.Seams = []Seam{base.Seams[0]}
		cur.Declarations = 1
		err := compare(base, cur)
		if err == nil || !strings.Contains(err.Error(), "从当前扫描中消失") {
			t.Fatalf("接缝消失应报错, got %v", err)
		}
	})
	t.Run("接缝新增未登记", func(t *testing.T) {
		cur := base
		cur.Seams = append(append([]Seam{}, base.Seams...), seam("c.New"))
		cur.Declarations = 3
		err := compare(base, cur)
		if err == nil || !strings.Contains(err.Error(), "未登记") {
			t.Fatalf("新增接缝应报错, got %v", err)
		}
	})
	t.Run("约束成文回退", func(t *testing.T) {
		cur := base
		cur.Seams = []Seam{seam("a.Iface", constraintReentry), seam("b.Func")}
		cur.FullyWritten = 0
		err := compare(base, cur)
		if err == nil || !strings.Contains(err.Error(), "成文回退") {
			t.Fatalf("约束消失应报错, got %v", err)
		}
	})
	t.Run("写齐总数回退", func(t *testing.T) {
		cur := base
		cur.FullyWritten = 0
		err := compare(base, cur)
		if err == nil || !strings.Contains(err.Error(), "只允许上升") {
			t.Fatalf("写齐总数下降应报错, got %v", err)
		}
	})
	t.Run("基线重复登记", func(t *testing.T) {
		bad := Report{Seams: []Seam{seam("a.Iface"), seam("a.Iface")}}
		if err := compare(bad, base); err == nil || !strings.Contains(err.Error(), "重复") {
			t.Fatalf("重复登记应报错, got %v", err)
		}
	})
}

func TestRatchetOnlyUp(t *testing.T) {
	old := Report{FullyWritten: 1, Seams: []Seam{seam("a.Iface", constraintReentry)}}
	up := Report{FullyWritten: 1, Seams: []Seam{seam("a.Iface", constraintReentry, constraintBlocking)}}
	if err := ratchetOnlyUp(old, up); err != nil {
		t.Fatalf("约束增加不应被拒: %v", err)
	}
	down := Report{FullyWritten: 0, Seams: []Seam{seam("a.Iface")}}
	if err := ratchetOnlyUp(old, down); err == nil || !strings.Contains(err.Error(), "消失") {
		t.Fatalf("约束减少应被拒, got %v", err)
	}
	shrunk := Report{Seams: nil}
	if err := ratchetOnlyUp(old, shrunk); err == nil || !strings.Contains(err.Error(), "消失") {
		t.Fatalf("接缝删除应被拒, got %v", err)
	}
}

func TestBaselineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	want := Report{
		Schema:       1,
		Declarations: 2,
		Callsites:    3,
		FullyWritten: 1,
		Seams:        []Seam{seam("a.Iface", constraintReentry, constraintBlocking, constraintPanic), seam("b.Func")},
	}
	if err := writeBaseline(path, want); err != nil {
		t.Fatalf("写基线: %v", err)
	}
	got, err := loadBaseline(path)
	if err != nil {
		t.Fatalf("读基线: %v", err)
	}
	if got.Schema != want.Schema || got.Declarations != want.Declarations ||
		got.Callsites != want.Callsites || got.FullyWritten != want.FullyWritten ||
		len(got.Seams) != len(want.Seams) {
		t.Fatalf("基线往返失真: got %+v want %+v", got, want)
	}
}

// TestRunUpdateThenCheck 用合成夹具走完整出货链：-update 写基线 → 再校验
// 必须为绿，且两次输出都是**同一组确定数字**（不是「同一源码自检为绿」那种
// 弱断言）。
func TestRunUpdateThenCheck(t *testing.T) {
	root := fixture(t)
	baseline := filepath.Join(t.TempDir(), "seam-baseline.json")
	var out, errb bytes.Buffer

	if code := run([]string{"-root", root, "-baseline", baseline, "-update"}, &out, &errb); code != 0 {
		t.Fatalf("-update 失败: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "接缝声明 2 / 调用点 3 / 三条约束写齐 1") {
		t.Fatalf("更新输出缺少确定数字: %s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"-root", root, "-baseline", baseline}, &out, &errb); code != 0 {
		t.Fatalf("同一夹具自检应为绿: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "基线：声明 2 / 调用点 3 / 写齐 1") {
		t.Fatalf("校验输出缺少基线对照: %s", out.String())
	}
}

func TestRunMissingBaseline(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-root", fixture(t), "-baseline", filepath.Join(t.TempDir(), "none.json")}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "不存在") {
		t.Fatalf("缺基线应失败并提示 -update: code=%d stderr=%s", code, errb.String())
	}
}

// TestRunUpdateRefusesRatchetDown 用合成夹具验证棘轮：手工登记一条夹具里
// 不存在的接缝，-update 必须拒绝写入——棘轮不是「跑一次 -update 就重置」的
// 装饰。
func TestRunUpdateRefusesRatchetDown(t *testing.T) {
	root := fixture(t)
	baseline := filepath.Join(t.TempDir(), "seam-baseline.json")
	if err := writeBaseline(baseline, Report{
		Schema: 1,
		Seams:  []Seam{seam("legacy.Gone", constraintPanic)},
	}); err != nil {
		t.Fatalf("写基线: %v", err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"-root", root, "-baseline", baseline, "-update"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "棘轮") {
		t.Fatalf("-update 不应接受接缝删除: code=%d stderr=%s", code, errb.String())
	}
}

func TestRunBadFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-nope"}, &out, &errb); code != 2 {
		t.Fatalf("未知 flag 应返回 2, got %d", code)
	}
}

func TestShortPkgPath(t *testing.T) {
	if got := shortPkgPath("example.com/mod/eventbus", "example.com/mod"); got != "eventbus" {
		t.Fatalf("shortPkgPath = %q", got)
	}
	if got := shortPkgPath("example.com/mod", "example.com/mod"); got != "mod" {
		t.Fatalf("根包 shortPkgPath = %q", got)
	}
}

func TestModulePathMissing(t *testing.T) {
	if _, err := modulePath(t.TempDir()); err == nil {
		t.Fatal("没有 go.mod 应报错")
	}
}

// ── 合成夹具（issue #433）──────────────────────────────────────────────
//
// 用例层**不扫真仓**：真仓扫描由 make seamcheck 门禁承担。夹具是临时目录里的
// 迷你 module，形状与真实仓库同构：声明接缝的包 / 跨包调用方 / 命令包
// （package main 不是宿主接缝）/ 本地 replace 引入的独立依赖 module。
//
// 夹具里不写任何领域词：它是契约的示例，不是真实业务。
const (
	fixtureModulePath = "example.com/fixture"
	fixtureDepPath    = "example.com/depmod"
)

// fixture 造出合成夹具，返回仓库根目录。
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	dep := filepath.Join(root, "depmod")
	write(t, filepath.Join(dep, "go.mod"), "module "+fixtureDepPath+"\n\ngo 1.24\n")
	write(t, filepath.Join(dep, "dep.go"), `package depmod

// Version 是合成依赖包里的一个常量（不引用任何接缝类型）。
const Version = "v1"
`)

	write(t, filepath.Join(root, "go.mod"), "module "+fixtureModulePath+
		"\n\ngo 1.24\n\nrequire "+fixtureDepPath+" v0.0.0\n\nreplace "+fixtureDepPath+" => ./depmod\n")

	write(t, filepath.Join(root, "api", "api.go"), `// Package api 是合成夹具里声明接缝的包。
package api

// Runner 是合成夹具里的一条接口接缝。
//
// 不得重入；不得阻塞；panic 隔离。
type Runner interface {
	// Run 执行一次动作。
	Run(id string) error
}

// Hook 是合成夹具里的一条函数类型接缝。
type Hook func(id string)
`)

	write(t, filepath.Join(root, "user", "user.go"), `// Package user 是合成夹具里的调用方包。
package user

import (
	"example.com/depmod"
	"example.com/fixture/api"
)

// Use 触发一次接口接缝调用。
func Use(r api.Runner) error { return r.Run("a") }

// UseAgain 再触发一次接口接缝调用。
func UseAgain(r api.Runner) error { return r.Run("b") }

// Call 触发一次函数类型接缝调用。
func Call(h api.Hook) { h("c") }

// Dep 用一下合成依赖（依赖存在，但它的调用点不计入本仓接缝）。
func Dep() string { return depmod.Version }
`)

	write(t, filepath.Join(root, "cmd", "tool", "main.go"), `// Command tool 是合成夹具里的命令包：package main 不是宿主接缝。
package main

// Runner 是命令包里的导出接口——命令工具不是宿主接缝，应被跳过。
type Runner interface {
	Run() error
}

func main() {}
`)
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("造目录 %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件 %s: %v", path, err)
	}
}

// TestCollectFixture 钉住合成夹具上的**确定数字**：声明 2 条接缝、3 个调用点、
// 1 条三条约束写齐（数字变了就是判据变了——不是「同一源码自检为绿」那种弱
// 断言）。
func TestCollectFixture(t *testing.T) {
	report, err := collect(fixture(t), loadPackages)
	if err != nil {
		t.Fatalf("扫描合成夹具: %v", err)
	}
	if report.Declarations != 2 || report.Callsites != 3 || report.FullyWritten != 1 {
		t.Fatalf("合成夹具的确定数字变了: 声明 %d / 调用点 %d / 写齐 %d（应为 2 / 3 / 1）",
			report.Declarations, report.Callsites, report.FullyWritten)
	}
	want := map[string]Seam{
		"api.Runner": {ID: "api.Runner", Kind: "interface", Callsites: 2, Written: constraintOrder},
		"api.Hook":   {ID: "api.Hook", Kind: "func", Callsites: 1},
	}
	if len(report.Seams) != len(want) {
		t.Fatalf("接缝集合大小 = %d, want %d（%+v）", len(report.Seams), len(want), report.Seams)
	}
	for _, s := range report.Seams {
		w, ok := want[s.ID]
		if !ok {
			t.Fatalf("夹具里不该有的接缝: %s", s.ID)
		}
		if s.Kind != w.Kind || s.Callsites != w.Callsites || len(s.Written) != len(w.Written) {
			t.Fatalf("接缝 %s = %+v, want %+v", s.ID, s, w)
		}
	}
}

// TestCollectFailsClosedWhenLoaderFails 钉住 fail-closed：包加载器拿不到结论
// 时**不得当作零接缝放行**（issue #433 的可注入接缝让这条能被机判）。
//
// 扫描根用合成夹具（**不是**空临时目录）：空目录会在 modulePath 一步就报错，
// 那样这条用例就断不到加载器身上——断言看着在，其实在测另一条路径。
func TestCollectFailsClosedWhenLoaderFails(t *testing.T) {
	report, err := collect(fixture(t), func(string) ([]*packages.Package, error) {
		return nil, fmt.Errorf("注入的加载失败")
	})
	if err == nil {
		t.Fatalf("加载器失败时应报错（不得当作零接缝放行），却拿到报告 %+v", report)
	}
}
