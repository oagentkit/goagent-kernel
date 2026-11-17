package deadline

// deadline 的公开判据面必须有生产消费方（issue #355 验收 2）。
//
// 此前 deadline 包零生产引用：导出 Route/Outcome/Status 三个公开面却没有任何
// 内核代码使用，宿主误以为它是「内核也在用的判据」而实际上它只活在测试里。
// 现在 finalize.Route 投影 deadline.Route（路由表唯一一份），本用例把
// 「至少一个非测试 import」钉成机器判据。

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteHasProductionConsumer(t *testing.T) {
	const want = "github.com/yi-nology/goagent-kernel/deadline"
	fset := token.NewFileSet()
	n := 0
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") ||
			strings.Contains(filepath.ToSlash(path), "/deadline/") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) == want {
				n++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描仓库 import: %v", err)
	}
	if n == 0 {
		t.Fatal("deadline 零生产引用：导出判据面必须至少有一个非测试消费方（当前应在 finalize.Route）")
	}
}
