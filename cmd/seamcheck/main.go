// Command seamcheck 统计「宿主接缝契约成文率」并做棘轮校验（issue #400，
// 补 #369 的统计面 / 给 #370 提供可机判基线）。
//
// # 什么算一条接缝
//
// **宿主接缝** = 宿主必须实现或注入的导出类型声明，本工具认两类：
//
//   - 导出 interface（至少一个方法）：宿主实现它；
//   - 导出命名函数类型（排除 *Option 配置函子）：宿主注入回调。
//
// 包级 main 不计（命令工具不是宿主接缝）。这是一条**可复现的**口径：同一份
// 源码扫两次必得同一集合；新增/删除接缝会改变清单，必须显式 -update 入基线
// （「清单缺一即红」）。
//
// # 三个数
//
//   - declarations：接缝声明数；
//   - callsites：**类型感知**的接缝调用点数（静态接收者类型是接缝 interface
//     的方法调用 + 静态类型是接缝函数类型的调用）——不是按方法名猜的近似值；
//   - fully_written：三条约束（不得重入 / 不得阻塞 / panic 隔离）在接缝文档
//     （interface 含方法注释）里**都写齐**的接缝数。
//
// # 棘轮
//
// 基线（缺省 seam-baseline.json）同时记录集合与每个接缝已写的约束：
//
//   - 当前接缝集合必须与基线**逐条相等**（少一条 / 多一条都红）；
//   - 每个接缝已写的约束只允许**增加**（基线写了 panic 隔离，当前就不能没有）；
//   - fully_written 总数只允许上升。
//
// -update 生成基线时同样拒绝回退（除非 -force）：棘轮不是「跑一次 -update
// 就重置」的装饰。
package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 约束键（基线里的稳定拼写；顺序即输出顺序）。
const (
	constraintReentry  = "reentry"
	constraintBlocking = "blocking"
	constraintPanic    = "panic"
)

var constraintOrder = []string{constraintReentry, constraintBlocking, constraintPanic}

// Seam 一条接缝的扫描结果。
type Seam struct {
	// ID 短包路径 + 类型名（如 eventbus.Store）；跨包同名类型靠它区分。
	ID string `json:"id"`
	// Kind interface | func。
	Kind string `json:"kind"`
	// Callsites 类型感知的调用点数。
	Callsites int `json:"callsites"`
	// Written 已写齐的约束键（canonical 顺序）。
	Written []string `json:"written,omitempty"`
}

// Report 一次扫描的完整结果（也是基线文件的格式）。
type Report struct {
	Schema       int    `json:"schema"`
	Declarations int    `json:"declarations"`
	Callsites    int    `json:"callsites"`
	FullyWritten int    `json:"fully_written"`
	Seams        []Seam `json:"seams"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("seamcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	baselinePath := fs.String("baseline", "seam-baseline.json", "基线文件路径")
	update := fs.Bool("update", false, "扫描并把当前结果写入基线")
	force := fs.Bool("force", false, "允许 -update 下调棘轮（删除接缝 / 减少已写约束）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report, err := collect(*root)
	if err != nil {
		reportf(stderr, "seamcheck: 扫描失败: %v\n", err)
		return 1
	}

	if *update {
		if !*force {
			if old, lerr := loadBaseline(*baselinePath); lerr == nil {
				if rerr := ratchetOnlyUp(old, report); rerr != nil {
					reportf(stderr, "seamcheck: 拒绝写入回退的基线（棘轮只允许上升；确需下调用 -force）:\n%v\n", rerr)
					return 1
				}
			}
		}
		if werr := writeBaseline(*baselinePath, report); werr != nil {
			reportf(stderr, "seamcheck: 写基线失败: %v\n", werr)
			return 1
		}
		printReport(stdout, report, nil)
		reportf(stdout, "  已写入基线 %s\n", *baselinePath)
		return 0
	}

	base, err := loadBaseline(*baselinePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			reportf(stderr, "seamcheck: 基线 %s 不存在；先运行 go run ./cmd/seamcheck -update 生成并评审\n", *baselinePath)
			return 1
		}
		reportf(stderr, "seamcheck: 读基线失败: %v\n", err)
		return 1
	}
	printReport(stdout, report, &base)
	if verr := compare(base, report); verr != nil {
		reportf(stderr, "seamcheck: 接缝契约基线不通过:\n%v\n", verr)
		return 1
	}
	return 0
}

// shortPkgPath 去掉 module 前缀，基线里的 ID 不随 module 迁移整体变脸。
func shortPkgPath(pkgPath, modPath string) string {
	if pkgPath == modPath {
		return filepath.Base(modPath)
	}
	return strings.TrimPrefix(strings.TrimPrefix(pkgPath, modPath), "/")
}

// modulePath 从 go.mod 读 module 路径（与 archcheck 同一条纪律：不写死）。
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", errors.New("go.mod 未找到 module 声明")
}
