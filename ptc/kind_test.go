// kind_test.go —— ErrorKind 词表的单一真相（issue #365）。
//
// toolfail 分类的幂等透传判据在 kind_passthrough_test.go（issue #364）。
//
// # 反向验证
//
//   - 新增一个 ErrorKind 常量但不登记 errorKindTable ⇒
//     TestErrorKindConstantsMatchRegistry 转红；
//   - 删掉 errorKindTable 里任一取值的 Suggestions ⇒
//     TestAllErrorKindsHaveClassAndSuggestions 转红。
package ptc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestAllErrorKindsHaveClassAndSuggestions 词表内每个取值在分类与建议两侧都有
// 明确归属，不得落 default（issue #365）。
func TestAllErrorKindsHaveClassAndSuggestions(t *testing.T) {
	kinds := AllErrorKinds()
	if len(kinds) < 14 {
		t.Fatalf("AllErrorKinds 只返回 %d 个取值，词表疑似塌方: %v", len(kinds), kinds)
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			info, ok := errorKindTable[kind]
			if !ok {
				t.Fatalf("ErrorKind %q 未登记 errorKindTable（分类与建议将同时落 default）", kind)
			}
			if info.Class == "" {
				t.Fatalf("ErrorKind %q 没有失败分类", kind)
			}
			if len(info.Suggestions) == 0 {
				t.Fatalf("ErrorKind %q 没有面向模型的修复建议", kind)
			}
			for _, s := range info.Suggestions {
				if strings.TrimSpace(s) == "" {
					t.Fatalf("ErrorKind %q 的建议含空串: %v", kind, info.Suggestions)
				}
			}
			if got, want := kindForErrorKind(kind), string(info.Class); got != want {
				t.Fatalf("kindForErrorKind(%q)=%q，应为 %q", kind, got, want)
			}
			if got := suggestionsFor(kind); len(got) == 0 {
				t.Fatalf("suggestionsFor(%q) 为空", kind)
			}
		})
	}
}

// TestErrorKindConstantsMatchRegistry 常量集与 errorKindTable 必须逐字一致：
// 新增常量不登记表、或表里出现幽灵取值，都必须红（issue #365）。
func TestErrorKindConstantsMatchRegistry(t *testing.T) {
	declared, err := scanErrorKindConstants()
	if err != nil {
		t.Fatalf("扫描 ErrorKind 常量失败: %v", err)
	}
	if len(declared) < 14 {
		t.Fatalf("只扫到 %d 个 ErrorKind 常量，判据可能已失效: %v", len(declared), declared)
	}

	registry := map[string]bool{}
	for _, kind := range AllErrorKinds() {
		registry[kind] = true
	}
	for name, value := range declared {
		if !registry[value] {
			t.Errorf("常量 %s=%q 未登记 errorKindTable/AllErrorKinds——"+
				"新增 ErrorKind 必须同时给分类与建议", name, value)
		}
	}
	for value := range registry {
		found := false
		for _, v := range declared {
			if v == value {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("errorKindTable 含幽灵取值 %q（没有对应的导出常量）", value)
		}
	}
}

// scanErrorKindConstants 扫本包生产代码里的 ErrorKind 常量（名 → 字符串值）。
func scanErrorKindConstants() (map[string]string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			return nil, perr
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if !strings.HasPrefix(id.Name, "ErrorKind") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					val, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						return nil, uerr
					}
					out[id.Name] = val
				}
			}
		}
	}
	return out, nil
}
