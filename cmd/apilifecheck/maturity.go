// maturity.go —— docs/MATURITY.md 成熟度盘点面 ↔ 清单 Experimental 集合的对账
// （issue #525：机制成熟度管理）。
//
// # 这条判据补的是什么缺口
//
// 评审（2026-11-27 §3.3 问题一 / §3.4 方向三）：不是所有包都该同时进 Stable，
// 但「维持演进」不能变成「永远没人盘」。晋级判据从「一个 minor 无 breaking」
// 一维升到三维（无 breaking 窗口 + 真实宿主接入证据 + 真实故障测试证据），
// 三维的**机器可判半边**就落在这里：标了「晋级候选」的包，三个证据格必须
// 都非空——没有证据的晋级候选只是愿望。
//
// # 三条判据
//
//  1. **双向覆盖**：清单里每个 Experimental 包必须有盘点行；盘点行不许指向
//     非 Experimental 包（晋级成 Stable 后行要撤——行与级别同生命周期）。
//  2. **值域**：五分类（晋级候选 / 维持演进 / 待合并 / 待降级 / 待废弃），
//     依据格非空（没有依据的分类无从复核）。
//  3. **晋级三维证据**：分类 = 晋级候选 ⇒ breaking 窗口 / 宿主接入 / 故障测试
//     三格全非空且不是「—」——这是 api-compat.md 晋级条件的执行体。
//
// # 判定口径
//
// 判定口径见 docs/api-compat.md「晋级条件」节与 docs/MATURITY.md 开篇；
// 本文件只做解析与判定，读文件与装配在 run/checkAll。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// maturityName 成熟度盘点面（相对仓库根）。
const maturityName = "docs/MATURITY.md"

// 五分类值域。
const (
	classPromote = "晋级候选"
	classKeep    = "维持演进"
	classMerge   = "待合并"
	classDemote  = "待降级"
	classRetire  = "待废弃"
)

// maturityClasses 全量五分类（顺序供报告用）。
var maturityClasses = []string{classPromote, classKeep, classMerge, classDemote, classRetire}

// maturityUpdatedRe 抓盘点面版本戳行。
var maturityUpdatedRe = regexp.MustCompile(`(?m)^updated:\s*(\S+)\s*$`)

// maturityRow 盘点表一行。
type maturityRow struct {
	Pkg      string
	Class    string
	Breaking string // breaking 窗口（晋级候选必填）
	Adoption string // 宿主接入证据（晋级候选必填）
	Fault    string // 故障测试证据（晋级候选必填）
	Basis    string // 依据（全行必填）
}

// parseMaturity 把 docs/MATURITY.md 的盘点表解析成行集合。
//
// 解析错误（缺表 / 缺列 / 版本戳缺失）返回 error：那是「给不出结论」（退出码 2）。
func parseMaturity(body string) ([]maturityRow, string, error) {
	m := maturityUpdatedRe.FindStringSubmatch(body)
	if m == nil {
		return nil, "", fmt.Errorf("%s 找不到版本戳行（顶部须有一行 `updated: YYYY-MM-DD`）", maturityName)
	}
	header := "| pkg | 分类 | breaking 窗口 | 宿主接入 | 故障测试 | 依据 |"
	lines := strings.Split(body, "\n")
	idx := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, "", fmt.Errorf("%s 找不到盘点表（表头须逐字为：%s）", maturityName, header)
	}
	if idx+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[idx+1]), "|---") {
		return nil, "", fmt.Errorf("%s 盘点表头之后缺分隔行（|---| 形态）", maturityName)
	}
	var rows []maturityRow
	for i := idx + 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := splitMaturityRow(line)
		if len(cells) < 6 {
			return nil, "", fmt.Errorf("%s 盘点表行只有 %d 列（须 6 列：%s），行内容：%s",
				maturityName, len(cells), strings.Trim(header, "|"), line)
		}
		rows = append(rows, maturityRow{
			Pkg:      cells[0],
			Class:    cells[1],
			Breaking: cells[2],
			Adoption: cells[3],
			Fault:    cells[4],
			Basis:    cells[5],
		})
	}
	if len(rows) == 0 {
		return nil, "", fmt.Errorf("%s 盘点表一行都没有（表头下没有数据行）", maturityName)
	}
	return rows, m[1], nil
}

// splitMaturityRow 按 `|` 切一行（剥首尾空段）。
func splitMaturityRow(line string) []string {
	cells := strings.Split(line, "|")
	if len(cells) > 0 && strings.TrimSpace(cells[0]) == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && strings.TrimSpace(cells[len(cells)-1]) == "" {
		cells = cells[:len(cells)-1]
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// checkMaturity 判盘点面与清单 Experimental 集合是否一致。
//
// 读不到盘点面 = 给不出结论（error，退出码 2）——盘点面是晋级判据的执行体，
// 缺了它「晋级候选有没有证据」没人判。
func checkMaturity(root string, m *Manifest) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(root, maturityName))
	if err != nil {
		return nil, fmt.Errorf("读不到成熟度盘点面 %s：%w", maturityName, err)
	}
	rows, updated, err := parseMaturity(string(body))
	if err != nil {
		return nil, err
	}
	var probs []string
	if _, dateErr := time.Parse("2006-01-02", updated); dateErr != nil {
		probs = append(probs, fmt.Sprintf(
			"%s 的 Updated %q 不是 YYYY-MM-DD：盘点面自己的版本戳都不可信", maturityName, updated))
	}

	// 实验集合：清单里全部 Experimental 包。
	experimental := map[string]bool{}
	for _, e := range m.Packages {
		if e.Level == LevelExperimental {
			experimental[e.Pkg] = true
		}
	}

	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Pkg] {
			probs = append(probs, fmt.Sprintf(
				"%s：包 %s 重复盘点——同一包两行，判到哪行无从定位", maturityName, r.Pkg))
		}
		seen[r.Pkg] = true

		if !experimental[r.Pkg] {
			probs = append(probs, fmt.Sprintf(
				"%s：盘点行 %s 不是清单里的 Experimental 包（晋级成 Stable / 已废弃的包要撤行——行与级别同生命周期）",
				maturityName, r.Pkg))
		}
		if !isKnownClass(r.Class) {
			probs = append(probs, fmt.Sprintf(
				"%s：包 %s 的分类 %q 不在五分类（%s）里——分类是盘点的承诺，值域外的取值读不懂",
				maturityName, r.Pkg, r.Class, strings.Join(maturityClasses, " / ")))
		}
		if strings.TrimSpace(r.Basis) == "" || r.Basis == "—" {
			probs = append(probs, fmt.Sprintf(
				"%s：包 %s 的依据格为空——没有依据的分类无从复核", maturityName, r.Pkg))
		}
		if r.Class == classPromote {
			for _, field := range []struct{ name, val string }{
				{"breaking 窗口", r.Breaking},
				{"宿主接入", r.Adoption},
				{"故障测试", r.Fault},
			} {
				if strings.TrimSpace(field.val) == "" || field.val == "—" {
					probs = append(probs, fmt.Sprintf(
						"%s：包 %s 标了晋级候选但「%s」证据为空——晋级三维（无 breaking 窗口 + 宿主接入 + 故障测试）缺一格就不是晋级，是愿望",
						maturityName, r.Pkg, field.name))
				}
			}
		}
	}
	for pkg := range experimental {
		if !seen[pkg] {
			probs = append(probs, fmt.Sprintf(
				"%s：清单里的 Experimental 包 %s 没有盘点行——「维持演进」不能变成「永远没人盘」（漏盘一个 = 那个包退出了成熟度管理）",
				maturityName, pkg))
		}
	}
	return probs, nil
}

// isKnownClass 判分类是否在五值域内。
func isKnownClass(c string) bool {
	for _, k := range maturityClasses {
		if c == k {
			return true
		}
	}
	return false
}
