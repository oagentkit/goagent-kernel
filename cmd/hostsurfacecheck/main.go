// Command hostsurfacecheck 判宿主接入面清单的绑定对账（issue #524）。
//
// # 它回答什么
//
// 评审（2026-11-27 §3.4 方向二）要的「统一运行契约」有一个最低形态：宿主视角
// 有一份全量三分类清单（必须接 / 可选 / 内部件），且 README/USAGE 教宿主 import
// 的东西不落在内部件里。这两句话此前只能靠人读 docs/HOST-SURFACE.md 得出结论
// ——而本仓的纪律是「事实要有主，判据不能靠人读」。本命令就是执行体。
//
// # 三条判据
//
//  1. **清单覆盖**：分类表覆盖 api-lifecycle 登记的全部生产包，三档值域，
//     职责格非空（漏一个包 = 那个包对宿主视角隐形）；
//  2. **主链六步**：创建 / 领取 / 执行 / 提交 / 恢复 / 介入齐全，每步
//     「宿主提供」「kernel 兑现」两格非空；
//  3. **文档 import 白名单**：README 与 USAGE 代码块里的 kernel 自 import
//     只许落点在「必须接 / 可选」——文档教宿主 import 内部件 = 把内部实现
//     当公共 API 卖。
//
// # 判定面：登记面 + 仓内事实
//
// 解析与判定走 internal/hostsurface（判据纯函数），本命令只做 I/O：读 md 与
// api-lifecycle yaml、抽文档代码块的 import、把结论转成退出码。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（文件读不到 / 解析不了）。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yi-nology/goagent-kernel/internal/hostsurface"
	"gopkg.in/yaml.v3"
)

// 默认文件（相对仓库根）。
const (
	defaultSurface   = "docs/HOST-SURFACE.md"
	defaultLifecycle = "api-lifecycle.yaml"
)

// kernelModulePrefix 本仓 module path 前缀（import 归一到清单口径）。
const kernelModulePrefix = "github.com/yi-nology/goagent-kernel/"

// 文档文件：README 与 USAGE 是「教宿主怎么接」的两份主文档。
var docFiles = []string{"README.md", "docs/USAGE.md"}

// fenceRe 抓围栏代码块（``` 起止；语言标记可有可无）。
var fenceRe = regexp.MustCompile("(?s)```[a-zA-Z]*\n(.*?)```")

// singleImportRe 抓单行 import："path"。
var singleImportRe = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hostsurfacecheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	surface := fs.String("surface", defaultSurface, "接入面登记面（相对仓库根）")
	lifecycle := fs.String("lifecycle", defaultLifecycle, "API 生命周期清单（相对仓库根）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	reg, err := loadSurface(filepath.Join(*root, *surface))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hostsurfacecheck: %v\n", err)
		return 2
	}
	registered, err := loadLifecycle(filepath.Join(*root, *lifecycle))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hostsurfacecheck: %v\n", err)
		return 2
	}
	imports, err := collectDocImports(*root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hostsurfacecheck: %v\n", err)
		return 2
	}

	res := hostsurface.Judge(reg, hostsurface.Facts{
		RegisteredPackages: registered,
		DocImports:         imports,
	})
	if len(res.Problems) > 0 {
		for _, msg := range res.Problems {
			_, _ = fmt.Fprintf(stderr, "违反：%s\n", msg)
		}
		_, _ = fmt.Fprintf(stderr, "接入面绑定对账未通过：%d 个步骤 / %d 个包分类 / %d 条文档 import\n",
			res.Steps, res.Packages, res.Imports)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "接入面绑定对账通过：%d 个步骤 / %d 个包分类 / %d 条文档 import\n",
		res.Steps, res.Packages, res.Imports)
	return 0
}

// loadSurface 读接入面登记面。
//
// 判定不在本层（结论全在 internal/hostsurface.Judge）——读真文件是门禁纪律
// 本身要求的（登记面必须是仓里那一份）。
func loadSurface(path string) (*hostsurface.Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到接入面登记面 %s：%w", path, err)
	}
	reg, err := hostsurface.ParseDoc(string(b))
	if err != nil {
		return nil, fmt.Errorf("接入面登记面 %s 解析不了：%w", path, err)
	}
	return reg, nil
}

// lifecycleDoc api-lifecycle.yaml 的最小形状（只取 pkg 全集）。
type lifecycleDoc struct {
	Packages []struct {
		Pkg string `yaml:"pkg"`
	} `yaml:"packages"`
}

// loadLifecycle 读 api-lifecycle 的包全集（覆盖判据的基准面）。
func loadLifecycle(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到 api-lifecycle %s：%w", path, err)
	}
	var doc lifecycleDoc
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(false) // 清单里有大量判据字段，这里只取 pkg
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("解析 api-lifecycle %s 失败：%w", path, err)
	}
	out := map[string]bool{}
	for _, p := range doc.Packages {
		if p.Pkg == "" {
			return nil, fmt.Errorf("api-lifecycle %s 有空 pkg 条目：包全集推不出来", path)
		}
		out[p.Pkg] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("api-lifecycle %s 一个包都没读到：空全集会让覆盖判据永远为真", path)
	}
	return out, nil
}

// collectDocImports 抽 README 与 USAGE 代码块里的 kernel 自 import，归一成
// 清单口径的相对包路径（去 module 前缀）。
//
// 只认 import 声明：正文里提到的包名、go get 命令行都不是「教宿主 import」。
// 读不到某份文档 = 给不出结论（exit 2），不静默跳过——少判一份文档的白名单
// 等于那半份没有门禁。
func collectDocImports(root string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, rel := range docFiles {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("读不到文档 %s：%w", rel, err)
		}
		for _, block := range fenceRe.FindAllStringSubmatch(string(b), -1) {
			for _, path := range importsInBlock(block[1]) {
				if path == "github.com/yi-nology/goagent-kernel" {
					// module 根不是包（根目录无生产 Go 文件）：按未登记处理，
					// 让判据去报「文档 import 了未登记包」。
					out["<module-root>"] = true
					continue
				}
				if strings.HasPrefix(path, kernelModulePrefix) {
					out[strings.TrimPrefix(path, kernelModulePrefix)] = true
				}
			}
		}
	}
	return out, nil
}

// importsInBlock 从一个代码块里抽 import 声明的路径（单行与分组两形态）。
func importsInBlock(block string) []string {
	var out []string
	for _, m := range singleImportRe.FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	// 分组形态：import ( ... )，逐行取引号路径。
	lines := strings.Split(block, "\n")
	inGroup := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inGroup {
			if trimmed == "import (" || strings.HasPrefix(trimmed, "import (") {
				inGroup = true
			}
			continue
		}
		if trimmed == ")" {
			inGroup = false
			continue
		}
		// 组内行：可带别名/点导入/下划线导入，取第一个引号串。
		if idx := strings.Index(trimmed, `"`); idx >= 0 {
			if end := strings.Index(trimmed[idx+1:], `"`); end >= 0 {
				out = append(out, trimmed[idx+1:idx+1+end])
			}
		}
	}
	return out
}
