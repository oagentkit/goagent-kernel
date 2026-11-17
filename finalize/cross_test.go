package finalize_test

// cross_test.go —— 收尾路由的**交叉断言**与「只有一份路由表」判据（issue #355）。
//
// # 为什么必须有交叉断言
//
// 同一份收尾事实此前有**两张路由表**（finalize 与 deadline），判据已分叉：
// 同一个接管的 ctx 在两包得到**相反**结论（shutdown vs takeover），而两包各钉
// 各的用例、**零交叉断言**——分叉可以无声发生（失效形态是绿的：两边都返回
// 正常值，只是结论相反）。
//
// 现在路由表只有一份（finalize），deadline.Route 是它的投影。本文件钉两件事：
//
//  1. 同一组 ctx 同时喂给两包，**两侧结论必须一致**（分叉的哨兵）；
//  2. 全仓只允许有一张收尾路由表（"两份都在"这件事本身要能被判出来）。
//
// # 反向验证
//
//   - 把 finalize.RouteWith 的接管臂删掉 ⇒ 用例 1 转红（接管 ctx 的结论不再是
//     接管，两侧也不再一致）；
//   - 让 deadline.Route 多认一个 cause ⇒ 用例 1 转红；
//   - 在别的包里另写一张收尾路由表 ⇒ 用例 2 转红。

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/deadline"
	"github.com/yi-nology/goagent-kernel/finalize"
	"github.com/yi-nology/goagent-kernel/steer"
)

// hostCancelSentinel 宿主自己的「用户取消」哨兵（内核发不出这个事实）。
var hostCancelSentinel = errors.New("caller asked to stop")

// outcomeOf 把 finalize 的路由臂映射成 deadline 的结论（测试侧的期望映射）。
//
// 映射本身写在这里（而不是从包里取）：它是「两包结论应当一致」的**判据**，
// 写死才能发现两侧悄悄长出第三个取值。
func outcomeOf(a finalize.Action) deadline.Outcome {
	switch a {
	case finalize.ActionTakeover:
		return deadline.OutcomeTakeover
	case finalize.ActionCancelled:
		return deadline.OutcomeCancelled
	case finalize.ActionTimedOut:
		return deadline.OutcomeTimedOut
	case finalize.ActionShutdown:
		return deadline.OutcomeShutdown
	default:
		return deadline.OutcomeNone
	}
}

// TestRouteAgreesWithDeadlineProjection 交叉断言：同一组 ctx 同时喂给两包，
// 两侧结论必须一致，且都等于该 ctx 应当得到的结论。
func TestRouteAgreesWithDeadlineProjection(t *testing.T) {
	cases := []struct {
		name        string
		ctx         context.Context
		sentinels   []error
		wantAction  finalize.Action
		wantOutcome deadline.Outcome
	}{
		{"接管", withCause(t, steer.ErrSteered), nil,
			finalize.ActionTakeover, deadline.OutcomeTakeover},
		{"会话超时", withCause(t, finalize.ErrSessionDeadline), nil,
			finalize.ActionTimedOut, deadline.OutcomeTimedOut},
		{"用户取消", withCause(t, finalize.ErrUserCancelled), nil,
			finalize.ActionCancelled, deadline.OutcomeCancelled},
		{"宿主注入的取消哨兵", withCause(t, hostCancelSentinel), []error{hostCancelSentinel},
			finalize.ActionCancelled, deadline.OutcomeCancelled},
		{"进程停机", plainCanceled(t), nil,
			finalize.ActionShutdown, deadline.OutcomeShutdown},
		{"正常（未取消）", context.Background(), nil,
			finalize.ActionFailed, deadline.OutcomeNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAction := finalize.RouteWith(tc.ctx, tc.sentinels...)
			if gotAction != tc.wantAction {
				t.Fatalf("finalize 结论 = %v，期望 %v", gotAction, tc.wantAction)
			}
			gotOutcome := deadline.Route(tc.ctx, tc.sentinels...)
			if gotOutcome != tc.wantOutcome {
				t.Fatalf("deadline 结论 = %v，期望 %v", gotOutcome, tc.wantOutcome)
			}
			// 分叉的哨兵：同一 ctx、同一时刻，两包结论必须一致。
			if want := outcomeOf(gotAction); gotOutcome != want {
				t.Fatalf("两包结论相反：finalize=%v（映射为 %v）而 deadline=%v——"+
					"同一 ctx 不得在两包得到相反结论", gotAction, want, gotOutcome)
			}
		})
	}
}

// TestSingleRouteTableInRepo 全仓只允许有一张收尾路由表（issue #355）。
//
// 「次序纪律（timeout 臂必须先于停机臂）只存在于一份判据里」——两份各写一遍的
// 后果不是冗余，是**分叉**：接管的 ctx 在两包得到相反结论，而零交叉断言时
// 分叉无声发生。合并后路由表只剩一张，本用例把它钉住：谁再加一张（哪怕换个
// 包名、换个函数名），当场红。
//
// 判据走 go/ast（不逐行正则看源码文本）：「这个函数体是不是一张路由表」是
// 结构性问题——它同时含**超时臂**（认 deadline 因果）与**停机臂**（ctx 已取消）。
func TestSingleRouteTableInRepo(t *testing.T) {
	root := repoRoot(t)
	var tables []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 跳过隐藏目录与依赖/产物目录：仓内的模块缓存（.gomodcache）里躺着
			// 第三方源码（含**故意写坏**的样例文件），扫它们既不是判据面，
			// 还会被解析失败带偏结论。
			if d.Name() != "." && (strings.HasPrefix(d.Name(), ".") ||
				d.Name() == "vendor" || d.Name() == "site" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		found, perr := routeTablesIn(path, root)
		if perr != nil {
			return perr
		}
		tables = append(tables, found...)
		return nil
	})
	if err != nil {
		t.Fatalf("扫仓库失败: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("收尾路由表必须只有一份（实得 %d）：\n  %s\n"+
			"同一份收尾事实两张表 ⇒ 判据必然分叉（接管的 ctx 在两包得到相反结论，"+
			"而零交叉断言时无声发生）。路由表的唯一收口是 finalize.Route/RouteWith。",
			len(tables), strings.Join(tables, "\n  "))
	}
}

// routeTablesIn 列出一个文件里「收尾路由表」的实现（超时臂 + 停机臂同现）。
func routeTablesIn(path, root string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	rel, rerr := filepath.Rel(root, path)
	if rerr != nil {
		rel = path
	}
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !hasDeadlineArm(fn.Body) || !hasShutdownArm(fn.Body) {
			continue
		}
		out = append(out, rel+": "+fn.Name.Name)
	}
	return out, nil
}

// hasDeadlineArm 函数体是否认会话超时因（引用 deadline 哨兵）。
func hasDeadlineArm(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:
			if node.Name == "ErrSessionDeadline" {
				found = true
			}
		case *ast.SelectorExpr:
			if node.Sel.Name == "ErrSessionDeadline" {
				found = true
			}
		}
		return true
	})
	return found
}

// hasShutdownArm 函数体是否有「ctx 已取消」臂（ctx.Err() != nil）。
func hasShutdownArm(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || bin.Op != token.NEQ {
			return true
		}
		call, ok := bin.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Err" {
			return true
		}
		if id, ok := bin.Y.(*ast.Ident); ok && id.Name == "nil" {
			found = true
		}
		return true
	})
	return found
}

// repoRoot 定位仓库根（向上找 go.mod）。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("找不到仓库根（go.mod）")
		}
		dir = parent
	}
}
