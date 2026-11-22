// hygiene —— 仓库卫生门禁：被跟踪的编译产物二进制不得入库（issue #413）。
//
// # 为什么要有这道门禁
//
// 2026-11-18 实录：仓库根跟踪了一个 3.5MB 的 Mach-O 二进制（cmd/docsync 的
// 本地产物，引入于 b469d8b）。危害有三层：
//
//   - 跨平台 checkout 拿到的是**不可执行的机器码**（它是 darwin/arm64 的）；
//   - 二进制与源码可能不同步——review 时看到的是一个改不动的 blob diff；
//   - 每次 clone 多下 3.5MB。
//
// 它是 `make docsync` 的产物，仓内已有**可复现的入口**，入库没有任何收益。
//
// # 为什么判「被跟踪」而不是「文件存在」
//
// 与 modwork / modreplace 同口径：本地构建产物留在工作树里是**正确状态**
// （.gitignore 收着就行），进了版本库才是缺陷。判「文件存在」会把「本地刚
// 构建完」报成违规——那是噪声，而噪声活不过第三个提交。
//
// # 为什么判魔数而不是扩展名
//
// 扩展名可以改：把 docsync 改名叫 docsync.bin 就绕过了扩展名判据。魔数是
// **内容事实**——ELF/Mach-O/PE/ar/wasm 的文件头写死了格式标识，改不改名都在。
//
// # 第二条判据：被跟踪却命中 .gitignore（force-add 形态，2026-11-22 补）
//
// 魔数只认**二进制**，而 `bin/` 与覆盖配置这类位置的本地产物是**文本**
// （coverage.out / bin/ 下的脚本），`git add -f` 强加进库时魔数一声不响——
// §16 第八阶段建议 3 点名的正是这两个位置「仍开着」。
//
// 判据取 git 自己的答案：`git ls-files -i -c --exclude-standard` = 被跟踪
// **且**命中忽略规则的文件。两个方向都判：① `git add -f` 强加了本地产物；
// ② 文件先入库、忽略规则后加（陈旧跟踪）——后者正是 .gitignore 头注里
// `/archcheck` 那次「忽略规则把整包静默排除」的同族形态，只是方向相反。
//
// 不自己解析 .gitignore：模式语义（否定、目录、嵌套 include）由 git 判，
// 复述一遍就是第二份会漂移的真相。
//
// # 失败形态
//
// 非零退出 + 逐条列出（路径 / 命中的格式 / 修法）。git 不可用时**报错退出**
// 而不是静默跳过——判据跑不起来却报绿，是最贵的失效形态。
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oagentkit/runtime/procx"
	"github.com/oekit/pkg/errors"
)

// binaryMagic 一条「这是编译产物」的判据：文件头前缀 + 格式名。
type binaryMagic struct {
	format string
	sig    []byte
}

// magics 可执行二进制 / 归档容器的文件头魔数。
//
// 只收**不会出现在源码或文档资源里**的格式：图片、字体、PDF 不在表内——
// 它们是合法的被跟踪资源，把它们判成违规就是在制造噪声。
var magics = []binaryMagic{
	{format: "ELF 可执行文件", sig: []byte{0x7f, 'E', 'L', 'F'}},
	{format: "Mach-O 64 位", sig: []byte{0xcf, 0xfa, 0xed, 0xfe}},
	{format: "Mach-O 32 位", sig: []byte{0xce, 0xfa, 0xed, 0xfe}},
	{format: "Mach-O 64 位（大端）", sig: []byte{0xfe, 0xed, 0xfa, 0xcf}},
	{format: "Mach-O 32 位（大端）", sig: []byte{0xfe, 0xed, 0xfa, 0xce}},
	{format: "Mach-O 通用二进制", sig: []byte{0xca, 0xfe, 0xba, 0xbe}},
	{format: "Windows PE 可执行文件", sig: []byte{'M', 'Z'}},
	{format: "ar 静态库", sig: []byte{'!', '<', 'a', 'r', 'c', 'h', '>', '\n'}},
	{format: "WebAssembly 模块", sig: []byte{0x00, 'a', 's', 'm'}},
}

// headBytes 读文件头这么多字节：最长的魔数是 ar 的 8 字节。
const headBytes = 8

// offender 一条违规：被跟踪的编译产物（魔数命中）或被跟踪却命中忽略规则
// （force-add / 陈旧跟踪，format 说明是哪一种）。
type offender struct {
	path   string
	format string
}

// detect 判定文件头属于哪种编译产物格式。
func detect(head []byte) (string, bool) {
	for _, m := range magics {
		if len(head) >= len(m.sig) && bytes.Equal(head[:len(m.sig)], m.sig) {
			return m.format, true
		}
	}
	return "", false
}

// readHead 读文件头（最多 headBytes 字节；短文件读到 EOF 不算错）。
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, headBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}

// check 逐个判被跟踪文件，返回违规清单。
//
// 文件不在工作树里（已删除但仍被跟踪）时跳过：内容无从判，而「已删除却
// 仍被跟踪」由 git status 报，不是本门禁的职责。
func check(root string, files []string) ([]offender, error) {
	var out []offender
	for _, rel := range files {
		head, err := readHead(filepath.Join(root, rel))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, errors.Wrap(err, "读文件头失败", "path", rel)
		}
		if format, ok := detect(head); ok {
			out = append(out, offender{path: rel, format: format})
		}
	}
	return out, nil
}

// gitOut 跑 git 子进程（**本地只读**子命令）。
//
// 全部 git 调用都从这里走：出口档位登记在 policy 的 egress_paths 段，
// 登记的是这一个函数——多一个 spawn 点就多一条要登记的边。
func gitOut(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// 子进程环境走唯一事实源 procx.ChildEnv，**不继承全量 environ**：本工具
	// 在 CI 里跑，父进程环境可能带凭据，而 git 的本地子命令用不到它们。
	// 附带收益是它默认补 GIT_TERMINAL_PROMPT=0——万一将来有人加了会问凭据的
	// 子命令，也是当场失败而不是把流水线挂死。
	cmd.Env = procx.ChildEnv(nil)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.Wrap(err, "git "+strings.Join(args, " ")+" 失败",
			"stderr", strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// repoRoot 取给定目录所属仓库的根。
func repoRoot(dir string) (string, error) {
	out, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.Wrap(err, "定位仓库根失败", "dir", dir)
	}
	return strings.TrimSpace(string(out)), nil
}

// trackedFiles 列出仓库里被跟踪的文件（路径相对仓库根）。
//
// 用 --full-name：路径恒相对仓库根，与调用点的当前目录无关。
func trackedFiles(root string) ([]string, error) {
	out, err := gitOut(root, "ls-files", "--full-name", "-z")
	if err != nil {
		return nil, errors.Wrap(err, "git ls-files 失败", "root", root)
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// report 把违规清单写成人读的报告。返回 true = 通过。
func report(w io.Writer, total int, bad []offender) bool {
	if len(bad) == 0 {
		_, _ = fmt.Fprintf(w, "hygiene: %d 个被跟踪文件，零编译产物二进制、零命中忽略规则的文件 ✓\n", total)
		return true
	}
	_, _ = fmt.Fprintf(w, "hygiene: 仓库跟踪了 %d 个不该入库的文件：\n", len(bad))
	for _, o := range bad {
		_, _ = fmt.Fprintf(w, "  %s（%s）\n", o.path, o.format)
	}
	_, _ = fmt.Fprintln(w, "  修法：git rm --cached <路径> 并删掉工作树副本；")
	_, _ = fmt.Fprintln(w, "        若它是本地产物，确认该路径已在 .gitignore（构建产物用 make 目标或 go build -o bin/ 产出）。")
	return false
}

// checkIgnored 列出「被跟踪且命中 .gitignore」的文件（force-add 形态）。
//
// 判据全交给 git（--exclude-standard 含 .gitignore / .git/info/exclude /
// core.excludesFile）：本仓不复述忽略规则的语义。git 起不来时报错，不静默返回空。
func checkIgnored(root string) ([]offender, error) {
	out, err := gitOut(root, "ls-files", "--full-name", "-i", "-c", "--exclude-standard", "-z")
	if err != nil {
		return nil, errors.Wrap(err, "git ls-files -i 失败", "root", root)
	}
	var bad []offender
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		bad = append(bad, offender{
			path:   p,
			format: "命中 .gitignore 却在版本库里（git add -f 强加，或忽略规则后加的陈旧跟踪）",
		})
	}
	return bad, nil
}

// run 执行一次卫生检查，返回进程退出码（0 = 通过）。
func run(root string, out io.Writer) int {
	files, err := trackedFiles(root)
	if err != nil {
		_, _ = fmt.Fprintln(out, "hygiene: "+err.Error())
		return 1
	}
	bad, err := check(root, files)
	if err != nil {
		_, _ = fmt.Fprintln(out, "hygiene: "+err.Error())
		return 1
	}
	ignored, err := checkIgnored(root)
	if err != nil {
		_, _ = fmt.Fprintln(out, "hygiene: "+err.Error())
		return 1
	}
	bad = append(bad, ignored...)
	if !report(out, len(files), bad) {
		return 1
	}
	return 0
}

func main() {
	root, err := repoRoot(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "hygiene: "+err.Error())
		fmt.Fprintln(os.Stderr, "hygiene: 判据跑不起来却报绿是最贵的失效形态——这里明确失败。")
		os.Exit(1)
	}
	os.Exit(run(root, os.Stdout))
}
