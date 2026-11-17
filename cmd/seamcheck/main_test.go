package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

func TestRunUpdateThenCheck(t *testing.T) {
	baseline := filepath.Join(t.TempDir(), "seam-baseline.json")
	var out, errb bytes.Buffer

	if code := run([]string{"-root", "../..", "-baseline", baseline, "-update"}, &out, &errb); code != 0 {
		t.Fatalf("-update 失败: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "接缝声明") {
		t.Fatalf("更新输出缺少统计摘要: %s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"-root", "../..", "-baseline", baseline}, &out, &errb); code != 0 {
		t.Fatalf("同一源码自检应为绿: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "基线：") {
		t.Fatalf("校验输出缺少基线对照: %s", out.String())
	}
}

func TestRunMissingBaseline(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-root", "../..", "-baseline", filepath.Join(t.TempDir(), "none.json")}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "不存在") {
		t.Fatalf("缺基线应失败并提示 -update: code=%d stderr=%s", code, errb.String())
	}
}

func TestRunUpdateRefusesRatchetDown(t *testing.T) {
	baseline := filepath.Join(t.TempDir(), "seam-baseline.json")
	// 手工登记一条当前源码里不存在的接缝：-update 会把它判成「接缝消失」
	// 并拒绝写入——棘轮不是「跑一次 -update 就重置」的装饰。
	if err := writeBaseline(baseline, Report{
		Schema: 1,
		Seams:  []Seam{seam("legacy.Gone", constraintPanic)},
	}); err != nil {
		t.Fatalf("写基线: %v", err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"-root", "../..", "-baseline", baseline, "-update"}, &out, &errb)
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
