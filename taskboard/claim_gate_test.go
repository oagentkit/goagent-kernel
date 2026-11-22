// claim_gate_test.go —— Board.Claim 的「词表外状态必须拒绝」契约判据（issue #360）。
//
// # 为什么判据要扫全仓
//
// #323 修了 coord.BoardAdapter 一个实现，契约文本与另外四个参考实现都没跟上
// （controlplane、coord、examples/reference-runtime；PG/SQLite 参考实现已迁至 goagent-runtime）。
// 只测某一个实现，下一个实现照抄契约时仍然会漏。故这里用 go/ast 扫
// **方法指纹**（签名匹配、名字可任意），断言每个 Board.Claim 的方法体都调用了
// `State.Claimable()`——判据是结构性的，不是「某几个已知文件里有这个字符串」。
//
// 反向验证：把任意一个实现里的 Claimable 调用去掉，本用例立即转红；
// 把契约注释里的 ErrUnknownState 删掉，契约文本用例转红。
package taskboard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestBoardClaimImplementationsUseClaimable 全部 Board.Claim 实现必须消费
// 单一判据 State.Claimable（issue #360）。
func TestBoardClaimImplementationsUseClaimable(t *testing.T) {
	root := ".."
	var scanned, claims []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "site") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		scanned = append(scanned, rel)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Claim" || fn.Recv == nil || fn.Body == nil {
				continue
			}
			if !isBoardClaimSignature(fn.Type) {
				continue
			}
			claims = append(claims, rel)
			if !bodyCallsClaimable(fn.Body) {
				t.Errorf("%s: Board.Claim 未调用 State.Claimable()——"+
					"词表外状态会照常领到租约（issue #360）", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 Board.Claim 实现失败: %v", err)
	}
	// 防假绿：扫描面本身失效（路径写错 / 跳过太多）时，claims 会变空而
	// 循环一条断言都不跑。2 是当前实现 taskboard.Board 签名的方法数
	// （coord / examples/reference-runtime；PG 与 SQLite 参考实现已于 v0.67.0
	// 迁至 goagent-runtime，其 Board.Claim 实现随仓走、那边自判）；
	// controlplane.MemStore.Claim 是运行时面签名（ttl time.Duration），
	// 由下面的专条判据覆盖。
	if len(claims) < 2 {
		t.Fatalf("只扫到 %d 个 Board.Claim 实现（扫描了 %d 个生产文件）：%v\n"+
			"判据已失效——实现改名/移动时必须同步本判据的指纹", len(claims), len(scanned), claims)
	}
}

// TestControlplaneMemStoreClaimUsesClaimable controlplane 的运行时面 Claim 不是
// taskboard.Board 的签名（第四参是 ttl），但它就是宿主实际调用的认领入口，
// 同样必须消费 Claimable（issue #360 实测复现点）。
func TestControlplaneMemStoreClaimUsesClaimable(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../controlplane/runtime.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 controlplane/runtime.go 失败: %v", err)
	}
	found := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Claim" || fn.Recv == nil || fn.Body == nil {
			continue
		}
		if receiverName(fn.Recv) != "MemStore" {
			continue
		}
		found = true
		if !bodyCallsClaimable(fn.Body) {
			t.Fatal("controlplane.MemStore.Claim 未调用 State.Claimable()——" +
				"脏状态任务会照常领到租约并铸令牌（issue #360）")
		}
	}
	if !found {
		t.Fatal("未找到 MemStore.Claim（判据已失效）")
	}
}

// receiverName 渲染接收者类型名（*MemStore / MemStore 都归到 MemStore）。
func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return lastTypeName(recv.List[0].Type)
}

// isBoardClaimSignature 判函数是否为 Board.Claim 的方法指纹：
// (ctx context.Context, id TaskID, agent AgentID, req ClaimRequest) (Task, error)。
//
// 刻意不判包限定名（taskboard.TaskID 与 TaskID 都接受）：实现可能在别的包，
// 也可能在 taskboard 包内；判的是契约形状而不是书写形态。
func isBoardClaimSignature(ft *ast.FuncType) bool {
	if ft.Params == nil || ft.Results == nil {
		return false
	}
	var params []string
	for _, field := range ft.Params.List {
		n := lastTypeName(field.Type)
		for range max(1, len(field.Names)) {
			params = append(params, n)
		}
	}
	if strings.Join(params, ",") != "Context,TaskID,AgentID,ClaimRequest" {
		return false
	}
	var results []string
	for _, field := range ft.Results.List {
		n := lastTypeName(field.Type)
		for range max(1, len(field.Names)) {
			results = append(results, n)
		}
	}
	return strings.Join(results, ",") == "Task,error"
}

// lastTypeName 取类型表达式的最右侧标识符（selector / 指针都剥掉）。
func lastTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return lastTypeName(t.X)
	}
	return ""
}

// bodyCallsClaimable 判方法体里是否出现 `.Claimable()` 调用。
func bodyCallsClaimable(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "Claimable" {
			found = true
			return false
		}
		return true
	})
	return found
}

// TestBoardClaimContractListsErrUnknownState 契约文本必须把 ErrUnknownState
// 列为 Claim 的必报错（issue #360：哨兵有、契约没写 = 照抄契约的实现不写闸）。
func TestBoardClaimContractListsErrUnknownState(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "taskboard.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 taskboard.go 失败: %v", err)
	}
	var doc string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Board" {
			return true
		}
		it, ok := ts.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		for _, m := range it.Methods.List {
			if len(m.Names) == 1 && m.Names[0].Name == "Claim" && m.Doc != nil {
				doc = m.Doc.Text()
				return false
			}
		}
		return true
	})
	if doc == "" {
		t.Fatal("未找到 Board.Claim 的契约注释（判据已失效）")
	}
	if !strings.Contains(doc, "ErrUnknownState") {
		t.Fatalf("Board.Claim 契约必须把 ErrUnknownState 列为必报错，实得：\n%s", doc)
	}
	if !strings.Contains(doc, "Claimable") {
		t.Fatalf("Board.Claim 契约应指向单一判据 State.Claimable，实得：\n%s", doc)
	}
}
