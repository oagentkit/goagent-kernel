package tenant

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/oekit/pkg/errors"
)

// immutabilityPromises 声明「**哪条用例承担不可变性承诺**」。
//
// 承诺 = 「改写授权面元素不得改穿回已注入的上下文」。承载这条承诺的用例
// 必须**真的用下标赋值改写授权面元素**——否则它的断言是空转的：
// append 在 len==cap 时必然重新分配，写入的是一个全新数组，
// **结构上不可能改穿** ⇒ 「共享切片」与「真拷贝」两种实现下断言都绿（假绿）。
//
// 为什么必须**按用例函数名限定**（而不是按包内总数统计）：
// 按总数统计时，包里**任意**一条下标赋值就能顶替承诺——把承载承诺的那条
// 用例里的改写删掉（断言变空转），守卫照样绿。
// 计数必须绑定到「承载承诺的那条用例」才有效。
//
// 名单是**显式**的：新增承载承诺的用例必须同时登记在这里，否则它的断言
// 无人守护（守卫还会因为「承诺用例不存在」而报红，不会静默失管）。
var immutabilityPromises = []string{
	// 改写**提取出的副本**的元素（改穿方向：机制面拿到的授权面被篡改）。
	"TestScopesElementIsImmutable",
	// 宿主在**注入后**改自己的原变量（改穿方向：宿主改穿已注入的上下文）。
	"TestInjectedScopesAreIsolatedFromCaller",
	// 提取副本的往返漂移（含同一份不可变性承诺）。
	"TestContextPassing",
}

// TestScopesElementIsImmutable 授权面必须**逐元素**不可变（issue #376）：
// 改写 From(ctx) 副本的元素不得改穿回已注入的上下文——否则机制面随后拿到的
// 授权面被篡改，Can 的子集判定随之改变 ⇒ 越权放行。
//
// 改写手法必须是**下标赋值**：append 在 len==cap 时必然重新分配，结构上不可能
// 改穿 ⇒ 用 append 的断言恒绿（假绿）。防这条回归见
// TestImmutabilityAssertionIsDiscriminating。
func TestScopesElementIsImmutable(t *testing.T) {
	ctx := WithContext(context.Background(), Context{
		Tenant:  "acme",
		Subject: "op-1",
		Scopes:  []string{scopeRead},
	})
	// 改写前：未授权的 scope 必须被拒（fail-closed）
	if _, err := Require(ctx, scopeOperate); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("改写前未授权的 scope 必须被拒，实得 %v", err)
	}
	got := From(ctx)
	got.Scopes[0] = scopeOperate // 下标赋值：共享即改穿
	// 改写后：机制面再取仍必须读到原值
	again := From(ctx)
	if len(again.Scopes) != 1 || again.Scopes[0] != scopeRead {
		t.Fatalf("授权面被改穿： %+v", again.Scopes)
	}
	// 改写后：授权判定必须仍是「拒」（不得越权放行）
	if _, err := Require(ctx, scopeOperate); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("改写副本不得影响授权判定（越权放行），实得 %v", err)
	}
}

// TestInjectedScopesAreIsolatedFromCaller 注入时授权面必须**定死**：
// 宿主在注入后改自己的原变量（同份配置模板再生成别的规则是最常见写法）
// 不得改穿已注入的上下文（issue #376 的构造方向）。
//
// 改写手法同样是**下标赋值**（append 形态的改写结构上不可能改穿 ⇒ 断言恒绿）。
func TestInjectedScopesAreIsolatedFromCaller(t *testing.T) {
	orig := Context{Tenant: "acme", Scopes: []string{scopeRead}}
	ctx := WithContext(context.Background(), orig)
	orig.Scopes[0] = scopeOperate // 宿主改自己的原变量：下标赋值，共享即改穿
	if got := From(ctx); len(got.Scopes) != 1 || got.Scopes[0] != scopeRead {
		t.Fatalf("注入后授权面必须定死（宿主改自己的原变量不得改穿）： %+v", got.Scopes)
	}
	if _, err := Require(ctx, scopeOperate); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("注入后授权面不得被宿主改穿（越权放行），实得 %v", err)
	}
}

// TestImmutabilityAssertionIsDiscriminating 防**假绿回归**（issue #376）：
// 承载不可变性承诺的用例必须用**下标赋值**改写授权面元素，不得用 append。
//
// 为什么这条要机器判：append 在 len==cap 时必然重新分配 ⇒ 写入的是一个全新
// 数组，**结构上不可能改穿**。这类断言在「共享切片」与「真拷贝」两种实现下
// 都绿——恒绿的断言等于把契约写成了散文，后续维护者会以为有测试兜底。
//
// 判据走 go/ast（结构性判定不逐行看源码文本）：扫全部用例文件，
//   - 找出**承载承诺的用例**（见 immutabilityPromises），判它们函数体里
//     有没有「改写 .Scopes 元素」的下标赋值——没有就报红；
//   - 找出所有「对 Scopes 的 append 调用」并报红。
//
// 计数为什么必须**绑定到用例函数名**：按包内总数统计时，包里任意一条下标
// 赋值就能顶替承诺 ⇒ 把承载承诺的用例里的改写删掉（断言变空转），守卫仍然
// 绿。那正是「用例里出现了那个符号，不等于用例守住了那个符号的契约」。
func TestImmutabilityAssertionIsDiscriminating(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("列用例文件失败: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("本包没有用例文件——本条判据无从谈起")
	}
	guarded := make(map[string]bool, len(immutabilityPromises))
	for _, name := range immutabilityPromises {
		guarded[name] = true
	}
	// found[用例名] = 该用例函数是否在本包里
	found := map[string]bool{}
	// indexAssigns[用例名] = 该用例函数体里「.Scopes[i] = ...」的条数
	indexAssigns := map[string]int{}
	for _, f := range files {
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, f, nil, 0)
		if perr != nil {
			t.Fatalf("解析 %s 失败: %v", f, perr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if guarded[name] {
				found[name] = true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range node.Lhs {
						idx, ok := lhs.(*ast.IndexExpr)
						if ok && isScopesField(idx.X) {
							indexAssigns[name]++
						}
					}
				case *ast.CallExpr:
					id, ok := node.Fun.(*ast.Ident)
					if !ok || id.Name != "append" {
						return true
					}
					for _, arg := range node.Args {
						if !isScopesField(arg) {
							continue
						}
						if guarded[name] {
							t.Errorf("%s 的 %s：承载不可变性承诺的用例不得用 append 改写 Scopes 元素"+
								"（len==cap 时必然重分配，结构上不可能改穿 ⇒ 这条断言恒绿）", f, name)
							continue
						}
						t.Errorf("%s 的 %s：改写 Scopes 元素不得用 append（len==cap 时必然重分配，"+
							"结构上不可能改穿 ⇒ 这条断言恒绿）", f, name)
					}
				}
				return true
			})
		}
	}
	for _, name := range immutabilityPromises {
		if !found[name] {
			t.Errorf("承载不可变性承诺的用例 %s 不存在（被改名或删除了？）——承诺无人承担，"+
				"不可变性断言随时可能变成空转", name)
			continue
		}
		if indexAssigns[name] == 0 {
			t.Errorf("承载不可变性承诺的用例 %s 里没有下标赋值（.Scopes[i] = ...）："+
				"它的不可变性断言已变成空转——append 形态的改写结构上不可能改穿，"+
				"共享切片与真拷贝两种实现下都绿（假绿）", name)
		}
	}
}

// isScopesField 判表达式是不是「某个 Scopes 字段」。
func isScopesField(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Scopes"
}
