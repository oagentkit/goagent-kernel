package deadline

// prodcaller_test.go —— 「公开 API 必须有生产调用点」的判据（issue #355）。
//
// # 为什么需要这条
//
// deadline 是**导出面**（Route / Outcome / Config / Anchor / Store）：宿主会
// 直接 import 它、用它的公开面写自己的收尾逻辑。此前它**零生产引用**——全仓
// 没有任何生产代码 import 它，于是「公开一份收尾判据面」这件事既没有用户，
// 也没有任何机器信号：它可以继续漂移、继续与真正的收口（finalize）分叉，
// 而构建全绿。
//
// 判据：全仓**非测试**代码里 import 本包的次数 ≥ 1。
//
// 为什么判「非测试」：测试引用只证明判据跑得起来，不证明这份 API 被真的
// 用着——本包自己的用例 import 它不算数（那是自己证明自己）。
//
// # 反向验证
//
// 把 examples/reference-runtime 的墙钟接线去掉 ⇒ 本用例必须转红。

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// deadlineImportPath 本包的 import 路径（判据的匹配面）。
const deadlineImportPath = "github.com/yi-nology/goagent-kernel/deadline"

// TestDeadlineHasProductionCaller 本包作为公开 API，必须有生产调用点。
func TestDeadlineHasProductionCaller(t *testing.T) {
	root := repoRoot(t)
	var callers []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() != "." && (strings.HasPrefix(d.Name(), ".") ||
				d.Name() == "vendor" || d.Name() == "site" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		found, perr := importsDeadline(path)
		if perr != nil {
			return perr
		}
		if found {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			callers = append(callers, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫仓库失败: %v", err)
	}
	if len(callers) == 0 {
		t.Fatalf("deadline 是导出面（宿主会直接 import 它），但全仓没有任何**生产代码**" +
			"import 它——公开一份没人用的收尾判据面，只会让它与真正的收口" +
			"（finalize）继续漂移，而没有任何机器信号。" +
			"要么给它接上一个生产调用点（如 examples/reference-runtime 的会话墙钟接线），" +
			"要么按 issue #355 的 (C) 把它删掉。")
	}
}

// importsDeadline 该文件是否 import 了本包（走 go/ast：import 声明有六种以上
// 书写形态，逐行正则必然漏）。
func importsDeadline(path string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	for _, im := range f.Imports {
		p, uerr := strconv.Unquote(im.Path.Value)
		if uerr != nil {
			continue
		}
		if p == deadlineImportPath {
			return true, nil
		}
	}
	return false, nil
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
