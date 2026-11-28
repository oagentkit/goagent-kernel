// policy 文件的 schema：规则、阈值、例外登记的反序列化目标。
//
// 这一层是**可导入包**的第一道边界：宿主仓要用同一套判据，就得能读同一份
// policy 格式。故类型全部导出，且 policy 文件路径不写死成
// "architecture-policy.yaml"——宿主那份可能叫别的名字。

package archcheck

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load 读取并解析一份 policy 文件。
//
// 解析失败一律响亮失败（返回 error），不静默降级：门禁给不出结论却显示成通过，
// 是这条工具最贵的失败形态。
func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// 空文件不是「没有规则」而是「没有门禁」：静默接受它等于让一次误清空变成全绿。
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("policy 文件 %s 是空的（空 policy 等于没有门禁，不当作合法输入）", path)
	}
	var pol Policy
	// **严格模式**：未知键直接报错（KnownFields），不静默丢弃。YAML 对拼错的键
	// 是静默忽略的，而每个键都对应一条判定面：`scope` 拼错 ⇒ 规则**一行都不扫**
	// （有执行体、severity: error、报告里计入条数，只是永不触发）；`import_exempt`
	// 拼错 ⇒ 该放的 import 全被拒（假红，作者只看到「我的例外没生效」）。
	// 失败形态全是门禁变绿或误诊，故在这里一次掐掉。
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&pol); err != nil {
		return nil, fmt.Errorf("解析 policy 失败（严格模式：未知键会被 YAML 静默丢弃，"+
			"而一个拼错的键等于一条判不动的声明，故这里直接报错）: %w", err)
	}
	pol.Path = path
	return &pol, nil
}

// Violation 是一处违反。
type Violation struct {
	Rule string // 规则 ID
	Path string // 相对仓库根的路径
	Line int    // 行号（0 表示不指向具体行）
	Text string // 人类可读说明
	Kind string // 违规形态（同一规则下的细分类别）
}

// FindRoot 从 start 向上查找含 architecture-policy.yaml 的目录。
//
// 为什么向上找而不是要求调用方传绝对路径：门禁要在这个目录下判定所有包，
// 而包在磁盘上的位置是随仓库克隆位置变的。
//
// 为什么找到之后还要**解析符号链接**：filepath.Walk **不跟随符号链接**——
// 若仓库根本身是软链（改名后留下的兼容软链、monorepo 里的软链检出、缓存检出），
// Walk 会把根当成「一个文件」，整棵子树一个文件都扫不到 ⇒ 门禁把「没扫到」
// 当成「没有违规」（假绿），反向也会把每条「登记了却找不到」都报成红（假红）。
// 两种都是「门禁给不出结论却显示成了结论」——本仓纪律里最贵的失效形态。
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "architecture-policy.yaml")); err == nil {
			return realPath(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到 architecture-policy.yaml（从 %s 向上查找失败）", dir)
		}
		dir = parent
	}
}

// realPath 解析路径里的符号链接，返回真实路径。
//
// 解析失败即报错（不静默退回原路径）：解析不了就意味着**扫不到真实内容**——
// 门禁给不出结论。此时退回逻辑路径只会让门禁扫空（假绿），不会更安全。
func realPath(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("解析真实路径失败（%s）：%w", p, err)
	}
	return real, nil
}

// repoRoot 找本仓根目录（供本包内的用例读真实 policy）。
func repoRoot() (string, error) { return FindRoot(".") }
