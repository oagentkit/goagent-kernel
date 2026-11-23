// makeindex.go —— Makefile 的「目标 → 验收路径标记」索引。
//
// # 为什么单独一份
//
// 入口存在性与反向对账都要读 Makefile，而 Makefile 的解析有它自己的坑
// （变量目标、recipe 行、注释）⇒ 把它收在一处，别让判据里散落两套读法——
// 两套读法必然漂移，而漂移的方向通常是「判据看不见自己以为看得见的东西」。
package acceptancepath

import (
	"strings"
)

// MarkPrefix 是 Makefile 里声明「本目标是某条验收路径的入口」的标记前缀。
//
// 形态：`<target>: ## 说明  #acceptance-path: <id>`（与目标同行）。
//
// **刻意不带空格**（`#acceptance-path:` 而不是 `# acceptance-path:`）：
// 带空格的形态正是散文里引用它时的写法（`# acceptance-path: <id>`），
// 两者字面相同 ⇒ 门禁分不出「目标声明了自己」与「说明文字里恰好写了这句话」。
// 少一个空格就把这两种形态彻底分开——代价是标记长得不常规，值得。
const MarkPrefix = "#acceptance-path:"

// makeIndex 是 Makefile 的两个索引。
type makeIndex struct {
	// targets 全部**字面量**目标名（不含变量目标）。
	targets map[string]bool
	// marks 目标名 → 它声明的验收路径 id。
	marks map[string]string
}

// readMakefileIndex 解析 Makefile，返回目标集合与验收路径标记。
//
// 只认**目标行**上的标记：写在别处的标记不知道归属哪个目标，对账就没有着落。
// 变量目标（`<$(VAR)>:`）与含空格的行一律跳过——本仓的验收入口都是字面量名。
func readMakeFileIndex(path string, readFile func(string) ([]byte, error)) (makeIndex, error) {
	data, err := readFile(path)
	if err != nil {
		return makeIndex{}, err
	}
	idx := makeIndex{targets: map[string]bool{}, marks: map[string]string{}}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		target := strings.TrimSpace(line[:colon])
		if !isPlainTarget(target) {
			continue
		}
		idx.targets[target] = true
		if id, ok := markInLine(line); ok {
			idx.marks[target] = id
		}
	}
	return idx, nil
}

// isPlainTarget 判断目标名是不是字面量（不含变量、不含空格）。
func isPlainTarget(target string) bool {
	if target == "" {
		return false
	}
	for _, r := range target {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// markInLine 从一行里取出验收路径标记的 id。
func markInLine(line string) (string, bool) {
	idx := strings.Index(line, MarkPrefix)
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimSpace(line[idx+len(MarkPrefix):])
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}
